// validation.go 集中放 API 层的输入校验辅助函数。
//
// 安全目标：所有来自 HTTP 请求体 / 查询参数、会被持久化（落到磁盘 JSON）或
// 直接送入仿真引擎 / FRR 配置的数据，在到达业务逻辑前都必须先经过这里校验。
//
// 设计原则：
//   - 仅做"安全必做"的校验（格式、范围、存在性），不重复 storage 已覆盖的 IP
//     校验（storage.ValidateIPConfig 是 IP 的单一事实来源）。
//   - 校验失败一律返回 400，错误信息不回显敏感内部细节。
//   - 对标识符只拒绝控制字符（\x00 \n \r），避免日志注入 / 存储异常，同时不过度
//     限制合法字符集导致前端既有用法被误伤。
package api

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"

	"ensp-lab/internal/topology"
)

// validateFinite 拒绝 NaN / Inf，避免非法坐标污染存储或前端渲染崩溃。
func validateFinite(f float64) bool {
	return !math.IsInf(f, 0) && !math.IsNaN(f)
}

const (
	maxNameLen     = 128
	maxIdentLen    = 64
	maxAnnoTextLen = 4096
)

// topoIDRe 拓扑 ID 的严格形态：仅字母/数字/下划线/连字符，长度 1-64。
// 与 storage.topoFilePath 的拒绝规则（/ \ ..）形成纵深防御，且对前端自动生成的
// 16 位十六进制 ID 友好。
var topoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// IsValidDeviceType 判断设备类型是否为系统已知枚举。
func IsValidDeviceType(t topology.DeviceType) bool {
	switch t {
	case topology.DeviceRouter, topology.DeviceSwitch, topology.DeviceL3Switch,
		topology.DeviceFirewall, topology.DeviceAC, topology.DeviceAP,
		topology.DevicePC, topology.DeviceClient, topology.DeviceServer,
		topology.DeviceCloud, topology.DeviceHub, topology.DeviceVTEP:
		return true
	}
	return false
}

// validateTopoID 校验拓扑 ID；允许空（由调用方自动生成）。
func validateTopoID(id string) error {
	if id == "" {
		return nil
	}
	if !topoIDRe.MatchString(id) {
		return fmt.Errorf("invalid topology id %q: must match [A-Za-z0-9_-] and length 1-64", id)
	}
	return nil
}

// validateIdent 校验一般标识符（设备/端口名等）：非空、长度受限、不含控制字符。
// 端口名允许含 / : . 等（如 GE0/0/0、Eth0/1），仅拒绝会破坏日志/存储的控制字符。
func validateIdent(v string, max int) error {
	if v == "" {
		return fmt.Errorf("identifier is required")
	}
	if len(v) > max {
		return fmt.Errorf("identifier too long (max %d)", max)
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return fmt.Errorf("identifier contains invalid control characters")
	}
	return nil
}

// validateName 校验展示名（拓扑名/设备名）：长度受限、不含控制字符。
func validateName(name string) error {
	if len(name) > maxNameLen {
		return fmt.Errorf("name too long (max %d)", maxNameLen)
	}
	if strings.ContainsAny(name, "\x00\n\r") {
		return fmt.Errorf("name contains invalid control characters")
	}
	return nil
}

// validateIP 校验 IPv4/IPv6 地址；空串表示"未设置/清除"，放行。
// 与 storage.ValidateIPConfig 使用同一解析口径（net.ParseIP），避免双重标准。
func validateIP(v string) error {
	if v == "" {
		return nil
	}
	if net.ParseIP(v) == nil {
		return fmt.Errorf("invalid IP address %q", v)
	}
	return nil
}

// validateCIDR 校验 OSPF network 声明必须是合法 CIDR。
// 这是防止 FRR 配置注入的第一道关口：非法 CIDR 会被原样写进 ospfd.conf。
func validateCIDR(v string) error {
	if v == "" {
		return fmt.Errorf("network is required")
	}
	if _, _, err := net.ParseCIDR(v); err != nil {
		return fmt.Errorf("invalid CIDR %q: %w", v, err)
	}
	return nil
}

// validateOSPFArea 校验 OSPF area：整数 0..4294967295 或点分十进制 A.B.C.D。
func validateOSPFArea(v string) error {
	if v == "" {
		return fmt.Errorf("area is required")
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 || n > 4294967295 {
			return fmt.Errorf("ospf area out of range: %d", n)
		}
		return nil
	}
	parts := strings.Split(v, ".")
	if len(parts) != 4 {
		return fmt.Errorf("invalid ospf area %q (expect integer or A.B.C.D)", v)
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return fmt.Errorf("invalid ospf area %q", v)
		}
	}
	return nil
}

// validateASN 校验 BGP AS 号：1..4294967295（0 与超界均拒绝）。
func validateASN(n uint32) error {
	if n == 0 || n > 4294967295 {
		return fmt.Errorf("invalid ASN %d (must be 1..4294967295)", n)
	}
	return nil
}

// validateTopologyPayload 对完整拓扑载荷做结构性校验：
//   - 设备类型必须已知
//   - 链路引用的源/目标设备必须存在（拒绝悬空链路导致引擎 nil 解引用）
//   - 端口名不含控制字符
//
// IP 合法性交给 storage.ValidateIPConfig 统一把关，这里不重复。
func validateTopologyPayload(t *topology.Topology) error {
	if err := validateName(t.Name); err != nil {
		return err
	}
	for id, d := range t.Devices {
		if !IsValidDeviceType(d.Type) {
			return fmt.Errorf("invalid device type %q for device %q", d.Type, id)
		}
		if err := validateName(d.Name); err != nil {
			return fmt.Errorf("device %q: %w", id, err)
		}
	}
	for _, l := range t.Links {
		if _, ok := t.Devices[l.SourceDevice]; !ok {
			return fmt.Errorf("link %q references unknown source device %q", l.ID, l.SourceDevice)
		}
		if _, ok := t.Devices[l.TargetDevice]; !ok {
			return fmt.Errorf("link %q references unknown target device %q", l.ID, l.TargetDevice)
		}
		if err := validateIdent(l.SourcePort, maxIdentLen); err != nil {
			return fmt.Errorf("link %q source port: %w", l.ID, err)
		}
		if err := validateIdent(l.TargetPort, maxIdentLen); err != nil {
			return fmt.Errorf("link %q target port: %w", l.ID, err)
		}
	}
	return nil
}

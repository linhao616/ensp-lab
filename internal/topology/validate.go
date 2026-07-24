package topology

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrInvalidIPConfig 表示拓扑含非法 IP 配置（IP/Gateway/SubnetMask 无法解析为合法 IPv4）。
// 由 FileStorage.CreateTopology/UpdateTopology 在校验失败时用 %w 包装，
// 供 API 层通过 errors.Is 识别并返回 HTTP 400（客户端配置错误）。
var ErrInvalidIPConfig = errors.New("invalid IP configuration")

// ValidateIPConfig 检查拓扑中所有设备的接口 IP 配置是否合法。
//
// 背景：曾出现 server-3 的 IP 被手写成 10.0.10.300（末段 >255，非法 IPv4），
// 加载时未拦截，直到运行时 Ping 才因 net.ParseIP 返回 nil 而返回 HTTP 500。
// 此函数把校验前置到拓扑创建/加载阶段，让脏数据在入口就被发现。
//
// 规则：
//   - 仅校验“非空但无法解析为合法 IP”的字段，未配置的空字段视为合法。
//   - IPAddress / Gateway 必须是可解析的 IPv4（net.ParseIP 非空）。
//   - SubnetMask 支持点分十进制（如 255.255.255.0）；若写成 CIDR（含 "/"）则跳过解析。
//
// 返回所有发现的问题（nil 或空切片表示配置合法）。
func ValidateIPConfig(t *Topology) []error {
	if t == nil || t.Devices == nil {
		return nil
	}
	var errs []error
	for devID, dev := range t.Devices {
		if dev == nil || dev.Interfaces == nil {
			continue
		}
		for ifaceName, iface := range dev.Interfaces {
			if iface == nil {
				continue
			}
			if iface.IPAddress != "" && net.ParseIP(iface.IPAddress) == nil {
				errs = append(errs, fmt.Errorf("device %q interface %q has invalid IP address %q", devID, ifaceName, iface.IPAddress))
			}
			if iface.Gateway != "" && net.ParseIP(iface.Gateway) == nil {
				errs = append(errs, fmt.Errorf("device %q interface %q has invalid gateway %q", devID, ifaceName, iface.Gateway))
			}
			if iface.SubnetMask != "" && !strings.Contains(iface.SubnetMask, "/") && net.ParseIP(iface.SubnetMask) == nil {
				errs = append(errs, fmt.Errorf("device %q interface %q has invalid subnet mask %q", devID, ifaceName, iface.SubnetMask))
			}
		}
	}
	return errs
}

// diagnostic_handlers.go 提供统一诊断网关（P1-D）。
//
// 这些端点把"真实仿真引擎（sim.Engine）/系统能力"暴露为结构化 JSON，
// 让前端只负责渲染、不再自己解析 CLI 文本或编造数据：
//
//	POST /api/diagnostic/:id/ping       对 dst 执行真实 ping，返回 rtt 统计
//	POST /api/diagnostic/:id/traceroute 真实拓扑路径发现，返回逐跳列表
//	POST /api/diagnostic/:id/dns        系统 DNS 解析（失败绝不编造 IP）
//
// 设计原则（与"消除前端假数据"目标一致）：
//   - 所有端点先做"源设备存在 + 已开机"校验，未开机一律 400，信息明确。
//   - 目标地址非法 IP 一律 400。
//   - 引擎/系统返回的结果原样结构化输出；output 字段仅作人类可读备份，
//     hops/rtt/ip 才是前端渲染的数据源。
//   - DNS 解析失败时如实返回 404 + 原始错误，前端据此显示"解析失败"，
//     而不是显示一个编造的 IP。
package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

// localDNSHosts 是可选的"本地 host/DNS 映射"覆盖表（运维可注入）。
//
// 仅用于在无外网环境提供"已声明的真实映射"——绝不编造随机 IP。
// 当前拓扑模型没有 domain→IP 的 host 字段，因此默认空表：sandbox 无网时
// DNS 如实返回 404，前端据此显示"解析失败"而非伪造 IP。
//
// 若未来在 topology.Device/Interface 中新增 host 映射字段，可在此处改为
// 从传入拓扑里查找，逻辑位置已预留。
var localDNSHosts = map[string]string{}

// lookupLocalDNS 在本地覆盖表中查找域名映射；找不到返回空串（调用方应 404）。
func lookupLocalDNS(domain string) string {
	if ip, ok := localDNSHosts[strings.ToLower(strings.TrimSpace(domain))]; ok && ip != "" {
		return ip
	}
	return ""
}

// resolvePoweredOnSrc 从拓扑中定位 src 设备并校验其已开机（Status == running）。
//
// 返回设备指针；下列情况已直接写出 HTTP 响应并返回 (nil, false)：
//   - 设备不存在 -> 404
//   - 设备存在但未开机 -> 400，错误信息含"未开机"供前端识别展示。
func (r *Router) resolvePoweredOnSrc(c *gin.Context, topo *topology.Topology, src string) (*topology.Device, bool) {
	dev, ok := topo.GetDevice(src)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("源设备 %s 不存在", src)})
		return nil, false
	}
	if dev.Status != topology.StatusRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("设备 %s 未开机，请先开机", src)})
		return nil, false
	}
	return dev, true
}

// resolveDstIP 把请求体里的 dst 解析成合法的目标 IP：
//   - 若 dst 是拓扑内某设备的 ID，取其首个"up 且带 IP"的接口地址；
//   - 若 dst 本身就是 IP，则原样使用。
//
// 无法得到合法 IP（设备无活动接口 / 非法地址）时返回 ("", false)。
func (r *Router) resolveDstIP(topo *topology.Topology, dst string) (string, bool) {
	if ip := net.ParseIP(dst); ip != nil {
		return dst, true
	}
	if dev, ok := topo.GetDevice(dst); ok {
		for _, iface := range dev.Interfaces {
			if iface.Status == "up" && iface.IPAddress != "" {
				return iface.IPAddress, true
			}
		}
	}
	return "", false
}

// isTopologyDevice 报告 dst 是否为拓扑内已声明设备。
//
// 用于区分「拓扑内目标」（允许诊断）与「拓扑外任意 IP」（受
// ENS_DIAG_ALLOW_EXTERNAL 开关约束，默认禁止），详见 V-03 修复。
func isTopologyDevice(t *topology.Topology, dst string) bool {
	_, ok := t.GetDevice(dst)
	return ok
}

// aclBlockFromDecision 把 CLIState ACL 评估器的 deny 结果转换为诊断面板可见的
// blockedBy 结构化字段（{device, acl, rule, direction}）；非 deny 返回 nil。
// 设计 §5 T05 / 拍板 #5：仅用于 diagnosticPing/diagnosticTraceroute 响应体。
func aclBlockFromDecision(dec cli.Decision) map[string]interface{} {
	if dec.Action != "deny" {
		return nil
	}
	ruleID := 0
	if dec.Rule != nil {
		ruleID = dec.Rule.ID
	}
	return map[string]interface{}{
		"device":    dec.DeviceID,
		"acl":       dec.ACLNum,
		"rule":      ruleID,
		"direction": string(dec.Direction),
	}
}

// diagnosticPingRequest 是 POST /api/diagnostic/:id/ping 的请求体。
type diagnosticPingRequest struct {
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	Count int    `json:"count"`
}

// diagnosticPing 对指定目标执行真实 ping（4 次探测），结构化返回 RTT 统计。
//
//	请求体: {"src":"pc1","dst":"192.168.1.2","count":4}
//	响应:   {"success":bool,"output":string,"rtt":{"min":f,"avg":f,"max":f,"loss":f}}
//
// success 取 Received>0；loss 为 Lost/Sent*100；Received==0 时 min/avg/max 置 0。
// output 为 PingResult.Details 用换行原样拼接，仅作人类可读备份。
func (r *Router) diagnosticPing(c *gin.Context) {
	topoID := c.Param("id")
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	var req diagnosticPingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Src == "" || req.Dst == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src 与 dst 均为必填"})
		return
	}

	count := req.Count
	if count <= 0 {
		count = 4
	}
	if count > 100 {
		count = 100
	}

	// 校验源设备存在且已开机（未开机 -> 400）。
	if _, ok := r.resolvePoweredOnSrc(c, t, req.Src); !ok {
		return
	}

	// 解析目标 IP（接受设备 ID 或 IP 字面量）；非法 -> 400。
	dstIP, ok := r.resolveDstIP(t, req.Dst)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("非法或无法解析的目标地址 %q", req.Dst)})
		return
	}

	// 安全（V-03）：对「拓扑外」目标（非拓扑内设备、任意外部 IP）默认禁止诊断，
	// 防止服务端被用作网络侦察 / DoS 放大跳板。显式设置 ENS_DIAG_ALLOW_EXTERNAL=1
	// 可放行（仅限可信网络）。
	if !externalDiagnosticsAllowed() && !isTopologyDevice(t, req.Dst) {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "外部目标诊断已禁用（默认安全策略）。如需对拓扑外地址诊断，请设置环境变量 ENS_DIAG_ALLOW_EXTERNAL=1",
		})
		return
	}

	eng, eerr := r.getOrCreateEngine(topoID)
	if eerr != nil {
		clientError(c, http.StatusInternalServerError, "internal server error", eerr)
		return
	}

	res, perr := eng.Ping(req.Src, dstIP)
	if perr != nil {
		clientError(c, http.StatusInternalServerError, "internal server error", perr)
		return
	}
	if res == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "引擎返回空结果"})
		return
	}

	// 计算 rtt 统计（基于真实的逐包 RTTMs）。
	var min, avg, max float64
	loss := 0.0
	success := res.Received > 0
	if len(res.RTTMs) > 0 {
		var sum float64
		min = res.RTTMs[0]
		max = res.RTTMs[0]
		for _, v := range res.RTTMs {
			sum += v
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		avg = sum / float64(len(res.RTTMs))
	}
	if res.Sent > 0 {
		loss = float64(res.Lost) / float64(res.Sent) * 100
	}
	if !success {
		// 不可达：RTT 无意义，置 0（前端据此区分失败态）。
		min, avg, max = 0, 0, 0
	}

	// P1-C T05：ACL 拦截下游可见性。命中 deny 时统一在响应体暴露 blockedBy，
	// 且 ACL 代表真实过滤 → 视为不可达（诚实占位，不伪造成功）。
	srcDevType := topology.DeviceType("")
	if sd, ok := t.GetDevice(req.Src); ok {
		srcDevType = sd.Type
	}
	pingState := r.getOrInitCLIState(t.ID, req.Src, srcDevType)
	registry := r.cliStateRegistry()
	pingPath := cli.ComputeL3Path(pingState, dstIP, t)
	pingFlow := cli.PacketTuple{SrcIP: cli.ResolveSourceIP(pingState, dstIP, t), DstIP: dstIP, Proto: "icmp"}
	blockedBy := aclBlockFromDecision(cli.EvaluatePathACL(registry, pingPath, pingFlow))
	if blockedBy != nil {
		success = false
		min, avg, max = 0, 0, 0
		loss = 100.0
	}

	resp := gin.H{
		"success": success,
		"output":  strings.Join(res.Details, "\n"),
		"rtt": gin.H{
			"min":  min,
			"avg":  avg,
			"max":  max,
			"loss": loss,
		},
	}
	if blockedBy != nil {
		resp["blockedBy"] = blockedBy
	}
	c.JSON(http.StatusOK, resp)
}

// diagnosticTraceroute 在真实拓扑图上做路径发现，逐跳返回设备与延迟。
//
//	请求体: {"src":"pc1","dst":"192.168.1.2"}
//	响应:   {"reachable":bool,"hops":[{"hop":int,"ip":string,"device":string,"rtt":float}]}
//
// reachable 来自 TracerouteResult.Reached；hops 来自 Hops（DeviceID→device，
// DelayMs→rtt）。不可达时 hops 为空数组、reachable=false。
func (r *Router) diagnosticTraceroute(c *gin.Context) {
	topoID := c.Param("id")
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Src == "" || req.Dst == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src 与 dst 均为必填"})
		return
	}

	// 校验源设备存在且已开机（未开机 -> 400）。
	if _, ok := r.resolvePoweredOnSrc(c, t, req.Src); !ok {
		return
	}

	// 解析目标 IP；非法 -> 400。
	dstIP, ok := r.resolveDstIP(t, req.Dst)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("非法或无法解析的目标地址 %q", req.Dst)})
		return
	}

	// 安全（V-03）：同 diagnosticPing，拓扑外目标默认禁止。
	if !externalDiagnosticsAllowed() && !isTopologyDevice(t, req.Dst) {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "外部目标诊断已禁用（默认安全策略）。如需对拓扑外地址诊断，请设置环境变量 ENS_DIAG_ALLOW_EXTERNAL=1",
		})
		return
	}

	eng, eerr := r.getOrCreateEngine(topoID)
	if eerr != nil {
		clientError(c, http.StatusInternalServerError, "internal server error", eerr)
		return
	}

	res, terr := eng.Traceroute(req.Src, dstIP, 30)
	if terr != nil {
		clientError(c, http.StatusInternalServerError, "internal server error", terr)
		return
	}
	if res == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "引擎返回空结果"})
		return
	}

	hops := make([]gin.H, 0, len(res.Hops))
	for _, h := range res.Hops {
		hops = append(hops, gin.H{
			"hop":    h.Hop,
			"ip":     h.IP,
			"device": h.DeviceID,
			"rtt":    h.DelayMs,
		})
	}

	// P1-C T05：ACL 拦截下游可见性。命中 deny 时统一在响应体暴露 blockedBy，
	// 且 ACL 代表真实过滤 → 视为不可达（诚实占位）。
	srcDevType := topology.DeviceType("")
	if sd, ok := t.GetDevice(req.Src); ok {
		srcDevType = sd.Type
	}
	trState := r.getOrInitCLIState(t.ID, req.Src, srcDevType)
	registry := r.cliStateRegistry()
	trPath := make([]string, 0, len(res.Hops)+1)
	trPath = append(trPath, req.Src)
	for _, h := range res.Hops {
		if h.DeviceID != "" {
			trPath = append(trPath, h.DeviceID)
		}
	}
	trFlow := cli.PacketTuple{SrcIP: cli.ResolveSourceIP(trState, dstIP, t), DstIP: dstIP, Proto: "icmp"}
	blockedBy := aclBlockFromDecision(cli.EvaluatePathACL(registry, trPath, trFlow))
	reachable := res.Reached
	if blockedBy != nil {
		reachable = false
	}

	resp := gin.H{
		"reachable": reachable,
		"hops":      hops,
	}
	if blockedBy != nil {
		resp["blockedBy"] = blockedBy
	}
	c.JSON(http.StatusOK, resp)
}

// diagnosticDNS 对指定域名执行系统 DNS 解析，返回真实公网 IP。
//
//	请求体: {"src":"pc1","domain":"www.example.com"}
//	成功:   {"ip":"<首个IP>","ips":[...]}
//	失败:   404 {"error":"DNS 解析失败：<原始错误>"}
//
// 失败时绝不返回假 IP：先查 localDNSHosts 本地覆盖表（如有预设则用之），
// 否则如实 404。src 未开机同样返回 400（与上面一致）。
func (r *Router) diagnosticDNS(c *gin.Context) {
	topoID := c.Param("id")
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	var req struct {
		Src    string `json:"src"`
		Domain string `json:"domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Src == "" || req.Domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src 与 domain 均为必填"})
		return
	}

	// 校验源设备存在且已开机（未开机 -> 400）。
	if _, ok := r.resolvePoweredOnSrc(c, t, req.Src); !ok {
		return
	}

	domain := strings.TrimSpace(req.Domain)

	// 本地覆盖表优先（不触网、非侦察），无论外部开关是否开启都可用。
	if ip := lookupLocalDNS(domain); ip != "" {
		c.JSON(http.StatusOK, gin.H{
			"ip":  ip,
			"ips": []string{ip},
		})
		return
	}

	// 安全（V-03）：外部 DNS 解析默认禁止，防止服务端被用作 DNS 侦察 oracle。
	// 显式设置 ENS_DIAG_ALLOW_EXTERNAL=1 方可对公网域名解析（仅限可信网络）。
	if !externalDiagnosticsAllowed() {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "外部 DNS 解析已禁用（默认安全策略）。如需解析公网域名，请设置环境变量 ENS_DIAG_ALLOW_EXTERNAL=1",
		})
		return
	}

	ips, lerr := net.LookupHost(domain)
	if lerr == nil && len(ips) > 0 {
		c.JSON(http.StatusOK, gin.H{
			"ip":  ips[0],
			"ips": ips,
		})
		return
	}

	// 系统解析失败：检查本地覆盖表（如有预设则用之，仍真实非伪造）。
	if ip := lookupLocalDNS(domain); ip != "" {
		c.JSON(http.StatusOK, gin.H{
			"ip":  ip,
			"ips": []string{ip},
		})
		return
	}

	// 诚实失败：不编造 IP，前端据此显示"解析失败"。
	c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("DNS 解析失败：%v", lerr)})
}

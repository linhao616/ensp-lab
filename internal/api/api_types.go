// api_types.go - HTTP API DTO 类型
// 仅保留请求/响应/数据传输对象；图展示层类型（GraphNode/Edge/Data 等）
// 已迁出到 internal/topology 包以避免循环依赖。
package api

import "time"

type CreateTopologyRequest struct {
	Name string `json:"name"`
}

type CreateTopologyResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DeviceCount int       `json:"device_count"`
	LinkCount   int       `json:"link_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type StartNodeRequest struct {
	DeviceID string `json:"device_id"`
}

type StartNodeResponse struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type ApplyOSPFConfigRequest struct {
	Network string `json:"network"`
	Area    string `json:"area"`
}

type ApplyOSPFConfigResponse struct {
	DeviceID string `json:"device_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type BGPNeighbor struct {
	IP       string `json:"ip"`
	RemoteAS uint32 `json:"remote_as"`
}

type ApplyBGPConfigRequest struct {
	LocalAS   uint32        `json:"local_as"`
	Neighbors []BGPNeighbor `json:"neighbors"`
}

type ApplyBGPConfigResponse struct {
	DeviceID string `json:"device_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type RouteInfo struct {
	Destination string `json:"destination"`
	NextHop     string `json:"nexthop"`
	Metric      int    `json:"metric"`
	Protocol    string `json:"protocol"`
}

type GetRoutesResponse struct {
	DeviceID string      `json:"device_id"`
	Routes   []RouteInfo `json:"routes"`
}

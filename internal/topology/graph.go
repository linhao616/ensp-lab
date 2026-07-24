// graph.go - 拓扑图数据展示层类型（GraphNode/Edge/Data/Cytoscape）
// 这些类型属于 topology 领域概念，由 Topology.ToGraphJson() 产出，
// 放在本包避免 internal/api <-> internal/topology 循环依赖。
package topology

import "encoding/json"

// GraphNode 表示拓扑图中一个设备节点。
type GraphNode struct {
	ID     string                 `json:"id"`
	Label  string                 `json:"label"`
	Type   string                 `json:"type,omitempty"`
	Status string                 `json:"status,omitempty"`
	Model  string                 `json:"model,omitempty"`
	X      float64                `json:"x,omitempty"`
	Y      float64                `json:"y,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

// GraphEdge 表示拓扑图中一条链路边。
type GraphEdge struct {
	ID     string                 `json:"id"`
	Source string                 `json:"source"`
	Target string                 `json:"target"`
	Label  string                 `json:"label,omitempty"`
	Type   string                 `json:"type,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

// GraphData 是图节点和边的集合。
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type CytoscapeNode struct {
	Data struct {
		ID     string                 `json:"id"`
		Label  string                 `json:"label"`
		Type   string                 `json:"type,omitempty"`
		Status string                 `json:"status,omitempty"`
		Model  string                 `json:"model,omitempty"`
		Data   map[string]interface{} `json:"data,omitempty"`
	} `json:"data"`
	Position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position,omitempty"`
}

type CytoscapeEdge struct {
	Data struct {
		ID     string                 `json:"id"`
		Source string                 `json:"source"`
		Target string                 `json:"target"`
		Label  string                 `json:"label,omitempty"`
		Type   string                 `json:"type,omitempty"`
		Data   map[string]interface{} `json:"data,omitempty"`
	} `json:"data"`
}

type CytoscapeGraph struct {
	Nodes []CytoscapeNode `json:"nodes"`
	Edges []CytoscapeEdge `json:"edges"`
}

func (g *GraphData) ToCytoscapeJSON() ([]byte, error) {
	cytoscape := CytoscapeGraph{}
	for _, node := range g.Nodes {
		cn := CytoscapeNode{}
		cn.Data.ID = node.ID
		cn.Data.Label = node.Label
		cn.Data.Type = node.Type
		cn.Data.Status = node.Status
		cn.Data.Model = node.Model
		cn.Data.Data = node.Data
		cn.Position.X = node.X
		cn.Position.Y = node.Y
		cytoscape.Nodes = append(cytoscape.Nodes, cn)
	}
	for _, edge := range g.Edges {
		ce := CytoscapeEdge{}
		ce.Data.ID = edge.ID
		ce.Data.Source = edge.Source
		ce.Data.Target = edge.Target
		ce.Data.Label = edge.Label
		ce.Data.Type = edge.Type
		ce.Data.Data = edge.Data
		cytoscape.Edges = append(cytoscape.Edges, ce)
	}
	return json.Marshal(cytoscape)
}

func (g *GraphData) ToG6JSON() ([]byte, error) {
	return json.Marshal(g)
}

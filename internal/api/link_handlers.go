// link_handlers.go 提供拓扑链路的增删改查端点。
//
//   - POST   /api/topologies/:id/links         新增链路
//   - PUT    /api/topologies/:id/links/:linkId 更新链路属性（类型/线缆/带宽/时延/状态）
//   - DELETE /api/topologies/:id/links/:linkId 删除链路
//
// 链路 ID 由 generateID() 生成（未指定时）。updateLink 只允许修改
// 白名单字段，避免前端覆盖关键标识。
package api

import (
	"net/http"

	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

func (r *Router) addLink(c *gin.Context) {
	id := c.Param("id")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	var link topology.Link
	if err := c.ShouldBindJSON(&link); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if link.ID == "" {
		link.ID = generateID()
	}
	// 连线合法性校验（按设备角色约束矩阵）—— 后端兜底
	src, ok1 := t.GetDevice(link.SourceDevice)
	dst, ok2 := t.GetDevice(link.TargetDevice)
	if !ok1 || !ok2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "源或目标设备不存在"})
		return
	}
	lt, allowed, msg := topology.AllowedLinkType(src.Type, dst.Type)
	if !allowed {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	// 前端未指定链路类型时，使用校验推导出的类型
	if link.LinkType == "" {
		link.LinkType = lt
	}
	t.AddLink(&link)
	r.store.UpdateTopology(t)
	c.JSON(http.StatusCreated, link)
}

func (r *Router) updateLink(c *gin.Context) {
	id := c.Param("id")
	linkId := c.Param("linkId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	var found *topology.Link
	for _, link := range t.GetLinks() {
		if link.ID == linkId {
			found = link
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Link not found"})
		return
	}
	var update struct {
		LinkType     string  `json:"link_type"`
		CableType    string  `json:"cable_type"`
		Bandwidth    int     `json:"bandwidth"`
		Delay        int     `json:"delay"`
		Status       string  `json:"status"`
		SourceLabelDX float64 `json:"source_label_dx"`
		SourceLabelDY float64 `json:"source_label_dy"`
		TargetLabelDX float64 `json:"target_label_dx"`
		TargetLabelDY float64 `json:"target_label_dy"`
	}
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if update.LinkType != "" {
		found.LinkType = topology.LinkType(update.LinkType)
	}
	if update.CableType != "" {
		found.CableType = topology.PortType(update.CableType)
	}
	if update.Bandwidth != 0 {
		found.Bandwidth = update.Bandwidth
	}
	if update.Delay != 0 {
		found.Delay = update.Delay
	}
	if update.Status != "" {
		found.Status = update.Status
	}
	found.SourceLabelDX = update.SourceLabelDX
	found.SourceLabelDY = update.SourceLabelDY
	found.TargetLabelDX = update.TargetLabelDX
	found.TargetLabelDY = update.TargetLabelDY
	r.store.UpdateTopology(t)
	c.JSON(http.StatusOK, found)
}

func (r *Router) deleteLink(c *gin.Context) {
	id := c.Param("id")
	linkId := c.Param("linkId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	t.RemoveLink(linkId)
	r.store.UpdateTopology(t)
	c.JSON(http.StatusNoContent, nil)
}

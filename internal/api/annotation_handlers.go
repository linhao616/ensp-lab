// annotation_handlers.go 提供画布自由文本标注的增删改查端点。
//
//   - POST   /api/topologies/:id/annotations                    新增标注
//   - PUT    /api/topologies/:id/annotations/:annotationId     更新文本/位置
//   - DELETE /api/topologies/:id/annotations/:annotationId     删除标注
//
// 标注用于在画布上放置任意说明文字（VXLAN 规划、网络拓扑注释等）。
// 文本为纯 TXT 格式，由前端 AnnotationLayer 渲染。
package api

import (
	"fmt"
	"net/http"
	"time"

	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

func (r *Router) addAnnotation(c *gin.Context) {
	id := c.Param("id")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	var anno topology.TextAnnotation
	if err := c.ShouldBindJSON(&anno); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if anno.ID == "" {
		anno.ID = generateID()
	}
	// 安全/健壮性：限制文本长度（防存储膨胀 / 渲染卡顿），坐标必须有限。
	if len(anno.Text) > maxAnnoTextLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("annotation text too long (max %d)", maxAnnoTextLen)})
		return
	}
	if !validateFinite(anno.PositionX) || !validateFinite(anno.PositionY) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid annotation position"})
		return
	}
	anno.CreatedAt = time.Now()
	// 安全/并发：先深拷贝再追加，避免直接改写 store 中存活的共享 *Topology
	// （被并发读 / 仿真引擎快照共享），造成数据竞争。
	t = t.Clone()
	t.Annotations = append(t.Annotations, &anno)
	r.store.UpdateTopology(t)
	c.JSON(http.StatusCreated, anno)
}

func (r *Router) updateAnnotation(c *gin.Context) {
	id := c.Param("id")
	annotationId := c.Param("annotationId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	// 安全/并发：先深拷贝，再在副本上定位并修改标注，避免直接改写共享对象。
	t = t.Clone()
	var found *topology.TextAnnotation
	for _, anno := range t.Annotations {
		if anno.ID == annotationId {
			found = anno
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Annotation not found"})
		return
	}
	var update struct {
		Text      string  `json:"text"`
		PositionX float64 `json:"position_x"`
		PositionY float64 `json:"position_y"`
		FontSize   int     `json:"font_size"`
		FontFamily string  `json:"font_family"`
		TextAlign  string  `json:"text_align"`
		BorderStyle string `json:"border_style"`
		Background  string  `json:"background"`
		Width      float64 `json:"width"`
		Height     float64 `json:"height"`
	}
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 安全/健壮性：更新时同样限制文本长度与坐标有限性。
	if update.Text != "" && len(update.Text) > maxAnnoTextLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("annotation text too long (max %d)", maxAnnoTextLen)})
		return
	}
	if !validateFinite(update.PositionX) || !validateFinite(update.PositionY) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid annotation position"})
		return
	}
	if update.Text != "" {
		found.Text = update.Text
	}
	found.PositionX = update.PositionX
	found.PositionY = update.PositionY
	// 样式字段：使用指针式判断（0/空字符串表示不修改，避免误覆盖已有样式）
	if update.FontSize != 0 {
		found.FontSize = update.FontSize
	}
	if update.FontFamily != "" {
		found.FontFamily = update.FontFamily
	}
	if update.TextAlign != "" {
		found.TextAlign = update.TextAlign
	}
	if update.BorderStyle != "" {
		found.BorderStyle = update.BorderStyle
	}
	if update.Background != "" {
		found.Background = update.Background
	}
	if update.Width != 0 {
		found.Width = update.Width
	}
	if update.Height != 0 {
		found.Height = update.Height
	}
	r.store.UpdateTopology(t)
	c.JSON(http.StatusOK, found)
}

func (r *Router) deleteAnnotation(c *gin.Context) {
	id := c.Param("id")
	annotationId := c.Param("annotationId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	// 安全/并发：先深拷贝再重建标注切片，避免直接改写共享对象。
	t = t.Clone()
	var filtered []*topology.TextAnnotation
	for _, anno := range t.Annotations {
		if anno.ID != annotationId {
			filtered = append(filtered, anno)
		}
	}
	t.Annotations = filtered
	r.store.UpdateTopology(t)
	c.JSON(http.StatusNoContent, nil)
}

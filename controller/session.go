package controller

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/model"
)

// dyt-103: 会话记录 API —— 同一账号跨设备同步会话列表与历史

// GetChatSessions 获取当前用户的会话列表（kind: chat | agent）
func GetChatSessions(c *gin.Context) {
	userId := c.GetInt(ctxkey.Id)
	kind := c.Query("kind")
	if kind != "chat" && kind != "agent" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "kind 必须为 chat 或 agent"})
		return
	}
	sessions, err := model.ListChatSessions(userId, kind)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	data := make([]gin.H, 0, len(sessions))
	for _, s := range sessions {
		var messages []map[string]any
		if s.Messages != "" {
			_ = json.Unmarshal([]byte(s.Messages), &messages)
		}
		data = append(data, gin.H{
			"id":         s.SessionId,
			"title":      s.Title,
			"messages":   messages,
			"updated_at": s.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

// UpsertChatSession 保存会话（按 user+kind+session_id 幂等）
func UpsertChatSession(c *gin.Context) {
	userId := c.GetInt(ctxkey.Id)
	var req struct {
		Kind      string          `json:"kind"`
		SessionId string          `json:"session_id"`
		Title     string          `json:"title"`
		Messages  json.RawMessage `json:"messages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	if (req.Kind != "chat" && req.Kind != "agent") || req.SessionId == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "kind/session_id 不能为空"})
		return
	}
	// 限长保护：单会话内容最多 2MB（超出截断提示，避免写入巨型 payload）
	messages := string(req.Messages)
	if len(messages) > 2<<20 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "会话内容过大（>2MB），请精简后重试"})
		return
	}
	if err := model.UpsertChatSession(&model.ChatSession{
		UserId:    userId,
		Kind:      req.Kind,
		SessionId: req.SessionId,
		Title:     req.Title,
		Messages:  messages,
	}); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// DeleteChatSession 删除会话
func DeleteChatSession(c *gin.Context) {
	userId := c.GetInt(ctxkey.Id)
	kind := c.Param("kind")
	sessionId := c.Param("sessionId")
	if (kind != "chat" && kind != "agent") || sessionId == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "kind/session_id 不能为空"})
		return
	}
	if err := model.DeleteChatSession(userId, kind, sessionId); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

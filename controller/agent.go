package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/model"
)

// streamAgentBridge: 通用 SSE 透传
func streamAgentBridge(c *gin.Context, path string, body map[string]any) {
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequest(http.MethodPost, config.AgentBridgeURL+path, bytes.NewReader(payload))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": fmt.Sprintf("Agent 服务不可达：%v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"success": false, "message": buf.String()})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "streaming unsupported"})
		return
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = c.Writer.Write(buf[:n])
			flusher.Flush()
		}
		if readErr != nil {
			break
		}
	}
}

// dyt-64: Agent 聊天代理 —— 生成用户 access token（工具调用鉴权），
// 转发到 pi-bridge，SSE 流式回传（文本 delta / 工具调用事件）
func AgentChat(c *gin.Context) {
	if config.AgentBridgeURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Agent 服务未配置（AGENT_BRIDGE_URL）",
		})
		return
	}

	var req struct {
		SessionId     string `json:"session_id"`
		Model         string `json:"model"`
		Message       string `json:"message"`
		TokenKey      string `json:"token_key"`
		ChannelId     int    `json:"channel_id"`
		ThinkingLevel string `json:"thinking_level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	if req.SessionId == "" || req.Model == "" || req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "session_id/model/message 不能为空"})
		return
	}

	// 工具调用凭据 = 当前登录用户自己的 access_token（不生成、不覆盖），
	// 登录管理员即获得管理全权限；bridge 每次请求更新该会话的工具凭据
	user, err := model.GetUserById(c.GetInt(ctxkey.Id), true)
	accessToken := ""
	if err == nil {
		accessToken = user.AccessToken
	}
	streamAgentBridge(c, "/chat", map[string]any{
		"session_id":     req.SessionId,
		"model":          req.Model,
		"message":        req.Message,
		"token_key":      req.TokenKey,
		"access_token":   accessToken,
		"channel_id":     req.ChannelId,
		"thinking_level": req.ThinkingLevel,
	})
}

// dyt-67: 恢复 Agent 会话输出（页面离开后回到 Agent 页时重放事件续传）
func AgentResume(c *gin.Context) {
	if config.AgentBridgeURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Agent 服务未配置（AGENT_BRIDGE_URL）"})
		return
	}
	var req struct {
		SessionId string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "session_id 不能为空"})
		return
	}
	streamAgentBridge(c, "/resume", map[string]any{"session_id": req.SessionId})
}

// dyt-70: 聊天后台会话 —— 轻量转发执行（不加载 pi agent），支持断点续传
func ChatSend(c *gin.Context) {
	if config.AgentBridgeURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "聊天后台服务未配置（AGENT_BRIDGE_URL）"})
		return
	}
	var req struct {
		SessionId     string          `json:"session_id"`
		Model         string          `json:"model"`
		Messages      json.RawMessage `json:"messages"`
		TokenKey      string          `json:"token_key"`
		ChannelId     int             `json:"channel_id"`
		ThinkingLevel string          `json:"thinking_level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	if req.SessionId == "" || req.Model == "" || len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "session_id/model/messages 不能为空"})
		return
	}
	var msgs []map[string]any
	if err := json.Unmarshal(req.Messages, &msgs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "messages 格式错误"})
		return
	}
	streamAgentBridge(c, "/chat/v1", map[string]any{
		"session_id":     req.SessionId,
		"model":          req.Model,
		"messages":       msgs,
		"token_key":      req.TokenKey,
		"channel_id":     req.ChannelId,
		"thinking_level": req.ThinkingLevel,
	})
}

// dyt-70: 聊天会话续传（重放当前轮事件 + 实时续推）
func ChatResume(c *gin.Context) {
	if config.AgentBridgeURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "聊天后台服务未配置（AGENT_BRIDGE_URL）"})
		return
	}
	var req struct {
		SessionId string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "session_id 不能为空"})
		return
	}
	fmt.Printf("[ChatResume] session=%s\n", req.SessionId)
	streamAgentBridge(c, "/chat/v1/resume", map[string]any{"session_id": req.SessionId})
}

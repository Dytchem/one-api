package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/model"
)

func checkTokenOwnership(c *gin.Context, tokenKey string) (int, bool) {
	userId := c.GetInt(ctxkey.Id)
	if tokenKey == "" {
		return userId, true
	}
	token, err := model.ValidateUserToken(tokenKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "令牌无效"})
		return 0, false
	}
	if token.UserId != userId {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "令牌不属于当前用户"})
		return 0, false
	}
	return userId, true
}

// streamAgentBridge: 通用 SSE 透传（客户端断开时取消对 bridge 的请求，避免资源泄漏；
// bridge 后台执行不受影响，刷新后续传机制照常工作）
func streamAgentBridge(c *gin.Context, path string, body map[string]any) {
	payload, err := json.Marshal(body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}
	httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, config.AgentBridgeURL+path, bytes.NewReader(payload))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// 注意：SSE 透传不能用总超时（bridge 侧 Agent 最长执行 5 分钟，30s 总超时会掐断长回复），
	// 依赖 c.Request.Context() 在客户端断开时取消；
	// 但拨号与响应头仍设超时，避免 bridge 半开/无响应时无限等待
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "Agent 服务不可达"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"success": false, "message": "bridge error"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "text/event-stream") {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "bridge returned non-SSE response"})
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "streaming unsupported"})
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := c.Writer.Write(buf[:n]); werr != nil {
				return
			}
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

	// 令牌归属校验：token_key 必须是当前登录用户自己的令牌
	userId, ok := checkTokenOwnership(c, req.TokenKey)
	if !ok {
		return
	}

	// 工具调用凭据 = 当前登录用户自己的 access_token（不生成、不覆盖），
	// 登录管理员即获得管理全权限；bridge 每次请求更新该会话的工具凭据
	user, err := model.GetUserById(userId, true)
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
		"user_id":        userId,
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
	streamAgentBridge(c, "/resume", map[string]any{"session_id": req.SessionId, "user_id": c.GetInt(ctxkey.Id)})
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

	// 令牌归属校验：token_key 必须是当前登录用户自己的令牌
	userId, ok := checkTokenOwnership(c, req.TokenKey)
	if !ok {
		return
	}
	streamAgentBridge(c, "/chat/v1", map[string]any{
		"session_id":     req.SessionId,
		"model":          req.Model,
		"messages":       msgs,
		"token_key":      req.TokenKey,
		"channel_id":     req.ChannelId,
		"thinking_level": req.ThinkingLevel,
		"user_id":        userId,
	})
}

// dyt-88: 停止 Agent 生成（用户点击停止按钮时通知 bridge 中止后台执行）
func AgentStop(c *gin.Context) {
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
	stopBridge(c, "/stop", map[string]any{"session_id": req.SessionId, "kind": "agent", "user_id": c.GetInt(ctxkey.Id)})
}

// dyt-88: 停止 Chat 生成
func ChatStop(c *gin.Context) {
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
	stopBridge(c, "/stop", map[string]any{"session_id": req.SessionId, "kind": "chat", "user_id": c.GetInt(ctxkey.Id)})
}

// stopBridge: 向 bridge 发送停止指令（非 SSE，普通 JSON 响应）
func stopBridge(c *gin.Context, path string, body map[string]any) {
	payload, err := json.Marshal(body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}
	httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, config.AgentBridgeURL+path, bytes.NewReader(payload))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "Agent 服务不可达"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"success": false, "message": string(respBody)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
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
	streamAgentBridge(c, "/chat/v1/resume", map[string]any{"session_id": req.SessionId, "user_id": c.GetInt(ctxkey.Id)})
}

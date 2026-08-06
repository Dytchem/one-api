package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/blacklist"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/network"
	"github.com/songquanpeng/one-api/model"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func authHelper(c *gin.Context, minRole int) {
	session := sessions.Default(c)
	username := session.Get("username")
	role := session.Get("role")
	id := session.Get("id")
	status := session.Get("status")
	if username == nil {
		// Check access token
		accessToken := c.Request.Header.Get("Authorization")
		if accessToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "无权进行此操作，未登录且未提供 access token",
			})
			c.Abort()
			return
		}
		user := model.ValidateAccessToken(accessToken)
		if user != nil && user.Username != "" {
			// Token is valid
			username = user.Username
			role = user.Role
			id = user.Id
			status = user.Status
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无权进行此操作，access token 无效",
			})
			c.Abort()
			return
		}
	} else {
		// dyt-96: cookie 会话按 id 回查数据库，防止降权/封禁后旧 cookie 权限残留
		// （session 里存的 role/status 是签发时的快照，最长 7 天不过期；
		// 黑名单内存态在服务重启后清空，必须回查 DB 才有最终裁决权）
		user, err := model.GetUserById(id.(int), false)
		if err != nil || user.Username == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "用户不存在或已被删除",
			})
			c.Abort()
			return
		}
		username = user.Username
		role = user.Role
		status = user.Status
		id = user.Id
	}
	// dyt-93: 已删除用户（status=3）同样拒绝，防止删除后旧 cookie/会话在服务重启后复活
	if status.(int) == model.UserStatusDisabled || status.(int) == model.UserStatusDeleted || blacklist.IsUserBanned(id.(int)) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户已被封禁",
		})
		session := sessions.Default(c)
		session.Clear()
		_ = session.Save()
		c.Abort()
		return
	}
	if role.(int) < minRole {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权进行此操作，权限不足",
		})
		c.Abort()
		return
	}
	c.Set("username", username)
	c.Set("role", role)
	c.Set("id", id)
	c.Next()
}

func UserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, model.RoleCommonUser)
	}
}

func AdminAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, model.RoleAdminUser)
	}
}

func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, model.RoleRootUser)
	}
}

func TokenAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		key := c.Request.Header.Get("Authorization")
		key = strings.TrimPrefix(key, "Bearer ")
		key = strings.TrimPrefix(key, "sk-")
		parts := strings.Split(key, "-")
		key = parts[0]
		token, err := model.ValidateUserToken(key)
		if err != nil {
			abortWithMessage(c, http.StatusUnauthorized, err.Error())
			return
		}
		if token.Subnet != nil && *token.Subnet != "" {
			if !network.IsIpInSubnets(ctx, c.ClientIP(), *token.Subnet) {
				abortWithMessage(c, http.StatusForbidden, fmt.Sprintf("该令牌只能在指定网段使用：%s，当前 ip：%s", *token.Subnet, c.ClientIP()))
				return
			}
		}
		userEnabled, err := model.CacheIsUserEnabled(token.UserId)
		if err != nil {
			abortWithMessage(c, http.StatusInternalServerError, err.Error())
			return
		}
		if !userEnabled || blacklist.IsUserBanned(token.UserId) {
			abortWithMessage(c, http.StatusForbidden, "用户已被封禁")
			return
		}
		requestModel, err := getRequestModel(c)
		if err != nil && shouldCheckModel(c) {
			abortWithMessage(c, http.StatusBadRequest, err.Error())
			return
		}
		c.Set(ctxkey.RequestModel, requestModel)
		if token.Models != nil && *token.Models != "" {
			c.Set(ctxkey.AvailableModels, *token.Models)
			if requestModel != "" && !isModelInList(requestModel, *token.Models) {
				abortWithMessage(c, http.StatusForbidden, fmt.Sprintf("该令牌无权使用模型：%s", requestModel))
				return
			}
		}
		c.Set(ctxkey.Id, token.UserId)
		c.Set(ctxkey.TokenId, token.Id)
		c.Set(ctxkey.TokenName, token.Name)
		if len(parts) > 1 {
			if model.IsAdmin(token.UserId) {
				c.Set(ctxkey.SpecificChannelId, parts[1])
			} else {
				abortWithMessage(c, http.StatusForbidden, "普通用户不支持指定渠道")
				return
			}
		}

		// dyt-62: 请求体 channel_id 指定渠道（聊天页固定渠道）
		if channelId := extractChannelId(c); channelId != "" {
			if !model.IsAdmin(token.UserId) {
				ch, err := model.GetChannelById(helper.String2Int(channelId), true)
				if err != nil {
					abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
					return
				}
				if ch.Status != model.ChannelStatusEnabled {
					abortWithMessage(c, http.StatusForbidden, "该渠道已被禁用")
					return
				}
				userGroup, err := model.CacheGetUserGroup(token.UserId)
				if err != nil || !channelGroupAllowed(ch.Group, userGroup) {
					abortWithMessage(c, http.StatusForbidden, "该渠道对当前用户不可用")
					return
				}
				if requestModel != "" && ch.Models != "" && !isModelInList(requestModel, ch.Models) {
					abortWithMessage(c, http.StatusForbidden, fmt.Sprintf("该渠道不支持模型：%s", requestModel))
					return
				}
			}
			c.Set(ctxkey.SpecificChannelId, channelId)
		}

		// set channel id for proxy relay
		// dyt-93: proxy 路径对非 admin 同样做启用 + 分组校验，防止越权使用任意渠道
		// （proxy 的 target 是任意路径，模型白名单无法校验，故仅校验渠道可用性与分组）
		// dyt-96: 非 admin 的 proxy 目标路径限白名单（标准 OpenAI 兼容端点），
		// 防止对内网渠道的管理端点/任意路径发起请求（SSRF 放大）
		if channelId := c.Param("channelid"); channelId != "" {
			if !model.IsAdmin(token.UserId) {
				if !isAllowedProxyPath(c.Request.URL.Path) {
					abortWithMessage(c, http.StatusForbidden, "普通用户仅可使用标准模型端点（chat/completions/embeddings/images/audio/models）")
					return
				}
				ch, err := model.GetChannelById(helper.String2Int(channelId), true)
				if err != nil {
					abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
					return
				}
				if ch.Status != model.ChannelStatusEnabled {
					abortWithMessage(c, http.StatusForbidden, "该渠道已被禁用")
					return
				}
				userGroup, err := model.CacheGetUserGroup(token.UserId)
				if err != nil || !channelGroupAllowed(ch.Group, userGroup) {
					abortWithMessage(c, http.StatusForbidden, "该渠道对当前用户不可用")
					return
				}
			}
			c.Set(ctxkey.SpecificChannelId, channelId)
		}

		c.Next()
	}
}

func shouldCheckModel(c *gin.Context) bool {
	if strings.HasPrefix(c.Request.URL.Path, "/v1/completions") {
		return true
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/chat/completions") {
		return true
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images") {
		return true
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/audio") {
		return true
	}
	return false
}

// isAllowedProxyPath: dyt-96 非 admin 经 proxy 路由可达的目标路径白名单
func isAllowedProxyPath(path string) bool {
	allowed := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/embeddings",
		"/v1/images",
		"/v1/audio",
		"/v1/models",
		"/v1/moderations",
	}
	for _, p := range allowed {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// dyt-62: 从请求体提取 channel_id 并移除（不随透传发给上游）。
// 聊天页通过 channel_id 固定使用指定渠道。
func channelGroupAllowed(channelGroup, userGroup string) bool {
	if channelGroup == "" {
		return true
	}
	for _, g := range strings.Split(channelGroup, ",") {
		if strings.TrimSpace(g) == userGroup {
			return true
		}
	}
	return false
}

func extractChannelId(c *gin.Context) string {
	requestBody, err := common.GetRequestBody(c)
	if err != nil || len(requestBody) == 0 {
		return ""
	}
	// dyt-93: 超大请求（>1MB）跳过 channel_id 提取，避免 map 反序列化+重序列化
	// 破坏 body（键序打乱/大整数精度丢失/重复键合并）；此时 channel_id 随透传发给上游，
	// 与旧版行为一致。聊天页正常请求体远小于该阈值。
	if len(requestBody) > 1<<20 {
		return ""
	}
	// dyt-96: 用 UseNumber 解码，重序列化时保持大整数/浮点原样，避免精度丢失
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(requestBody))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return ""
	}
	val, ok := data["channel_id"]
	if !ok {
		return ""
	}
	var idStr string
	switch v := val.(type) {
	case string:
		idStr = v
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			idStr = strconv.FormatInt(n, 10)
		}
	default:
		return ""
	}
	delete(data, "channel_id")
	if newBody, err := json.Marshal(data); err == nil {
		c.Set(ctxkey.KeyRequestBody, newBody)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(newBody))
	}
	return idStr
}

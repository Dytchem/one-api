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
	}
	if status.(int) == model.UserStatusDisabled || blacklist.IsUserBanned(id.(int)) {
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
		if channelId := c.Param("channelid"); channelId != "" {
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
	var data map[string]any
	if err := json.Unmarshal(requestBody, &data); err != nil {
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
	case float64:
		if v > 0 {
			idStr = strconv.Itoa(int(v))
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

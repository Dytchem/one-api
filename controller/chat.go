package controller

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/model"
)

// dyt-62: 聊天页渠道列表 —— 返回当前用户组可用的启用渠道（不含 key）
func GetChatChannels(c *gin.Context) {
	userId := c.GetInt(ctxkey.Id)
	group, err := model.CacheGetUserGroup(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	var channels []*model.Channel
	err = model.DB.Omit("key").
		Where("status = ?", model.ChannelStatusEnabled).
		Order("id asc").
		Find(&channels).Error
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	var list []gin.H
	for _, ch := range channels {
		if !channelGroupAllowed(ch.Group, group) {
			continue
		}
		var models []string
		if ch.Models != "" {
			for _, m := range strings.Split(ch.Models, ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					models = append(models, m)
				}
			}
		}
		list = append(list, gin.H{
			"id":     ch.Id,
			"name":   ch.Name,
			"type":   ch.Type,
			"models": models,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    list,
	})
}

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

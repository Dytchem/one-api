package middleware

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channeltype"
)

type ModelRequest struct {
	Model string `json:"model" form:"model"`
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		userId := c.GetInt(ctxkey.Id)
		userGroup, _ := model.CacheGetUserGroup(userId)
		c.Set(ctxkey.Group, userGroup)
		var requestModel string
		var channel *model.Channel
		channelId, ok := c.Get(ctxkey.SpecificChannelId)
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
				return
			}
			if channel.Status != model.ChannelStatusEnabled {
				abortWithMessage(c, http.StatusForbidden, "该渠道已被禁用")
				return
			}
		} else {
			requestModel = c.GetString(ctxkey.RequestModel)
			var err error

			// Health-aware selection: get all top-priority abilities, filter degraded, pick best
			abilities, abilityErr := model.GetTopSatisfiedAbilities(userGroup, requestModel)
			if abilityErr == nil && len(abilities) > 0 {
				// dyt-100: 一次 FilterAbilitiesWithScores 同时拿到健康分，供加权复用（免二次计算）
				scores, filtered := monitor.FilterAbilitiesWithScores(abilities, nil)
				if len(filtered) > 0 {
					// dyt-93: 健康度最高的前 k 个按 score×weight 加权随机选择——
					// 原实现固定取 filtered[0]，全部流量压向单一"最健康"渠道（Weight 失效 + 故障雪崩）
					k := 3
					if len(filtered) < k {
						k = len(filtered)
					}
					pool := filtered[:k]
					var total float64
					weights := make([]float64, len(pool))
					candidates := make([]*model.Channel, len(pool))
					for i, a := range pool {
						// dyt-100: 渠道优先从内存缓存取（MemoryCacheEnabled 时免 3 次查库），
						// 缓存未开启/未命中回退查库
						ch, cerr := model.CacheGetChannelById(a.ChannelId, true)
						if cerr != nil || ch.Status != model.ChannelStatusEnabled {
							weights[i] = 0
							continue
						}
						candidates[i] = ch
						w := scores[a.ChannelId] // 复用 FilterAbilities 已算好的健康分
						w = w*w + 0.1            // 退化渠道保留小概率
						if ch.Weight != nil && *ch.Weight > 0 {
							w *= float64(*ch.Weight)
						}
						weights[i] = w
						total += w
					}
					if total > 0 {
						r := rand.Float64() * total
						pick := 0
						for i, w := range weights {
							if w <= 0 {
								continue
							}
							r -= w
							if r <= 0 {
								pick = i
								break
							}
						}
						if candidates[pick] != nil {
							channel = candidates[pick]
						}
					}
				}
			}

			// Fallback to original random selection if health-aware path failed
			if channel == nil {
				channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, requestModel, false)
			}

			if err != nil {
				message := fmt.Sprintf("当前分组 %s 下对于模型 %s 无可用渠道", userGroup, requestModel)
				if channel != nil {
					logger.SysError(fmt.Sprintf("渠道不存在：%d", channel.Id))
					message = "数据库一致性已被破坏，请联系管理员"
				}
				abortWithMessage(c, http.StatusServiceUnavailable, message)
				return
			}
		}
		logger.Debugf(ctx, "user id %d, user group: %s, request model: %s, using channel #%d", userId, userGroup, requestModel, channel.Id)
		SetupContextForSelectedChannel(c, channel, requestModel)
		c.Next()
	}
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) {
	c.Set(ctxkey.Channel, channel.Type)
	c.Set(ctxkey.ChannelId, channel.Id)
	c.Set(ctxkey.ChannelName, channel.Name)
	if channel.SystemPrompt != nil && *channel.SystemPrompt != "" {
		c.Set(ctxkey.SystemPrompt, *channel.SystemPrompt)
	}
	c.Set(ctxkey.ModelMapping, channel.GetModelMapping())
	c.Set(ctxkey.OriginalModel, modelName) // for retry
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))
	c.Set(ctxkey.BaseURL, channel.GetBaseURL())
	cfg, _ := channel.LoadConfig()
	// this is for backward compatibility
	if channel.Other != nil {
		switch channel.Type {
		case channeltype.Azure:
			if cfg.APIVersion == "" {
				cfg.APIVersion = *channel.Other
			}
		case channeltype.Xunfei:
			if cfg.APIVersion == "" {
				cfg.APIVersion = *channel.Other
			}
		case channeltype.Gemini:
			if cfg.APIVersion == "" {
				cfg.APIVersion = *channel.Other
			}
		case channeltype.AIProxyLibrary:
			if cfg.LibraryID == "" {
				cfg.LibraryID = *channel.Other
			}
		case channeltype.Ali:
			if cfg.Plugin == "" {
				cfg.Plugin = *channel.Other
			}
		}
	}
	c.Set(ctxkey.Config, cfg)
}

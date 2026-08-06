package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channeltype"
)

func GetAllChannels(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	order := c.Query("order")
	sort := c.Query("sort")
	size := config.ItemsPerPage
	if s := c.Query("size"); s != "" {
		if parsedSize, err := strconv.Atoi(s); err == nil && parsedSize > 0 {
			size = parsedSize
		}
	}
	channels, err := model.GetAllChannels(p*size, size, "limited", order, sort)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
	return
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	channels, err := model.SearchChannels(keyword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

func AddChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel.CreatedTime = helper.GetTimestamp()
	keys := strings.Split(channel.Key, "\n")
	channels := make([]model.Channel, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		localChannel := channel
		localChannel.Key = key
		channels = append(channels, localChannel)
	}
	err = model.BatchInsertChannels(channels)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

// CloneChannel 复制渠道：保留全部配置与 Key，重置运行时状态
func CloneChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel.Id = 0
	channel.Name = channel.Name + " (复制)"
	channel.CreatedTime = helper.GetTimestamp()
	channel.Status = 1
	channel.ResponseTime = 0
	channel.TestTime = 0
	channel.Balance = 0
	channel.BalanceUpdatedTime = 0
	channel.UsedQuota = 0
	if err = model.DB.Create(channel).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if err = channel.AddAbilities(); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel.Id,
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channel := model.Channel{Id: id}
	err := channel.Delete()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func FetchChannelModels(c *gin.Context) {
	var req struct {
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
		Type    int    `json:"type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请求参数错误: " + err.Error(),
		})
		return
	}

	if req.BaseURL == "" {
		// Try to look up from channel type if provided
		if req.Type >= 0 && req.Type < len(channeltype.ChannelBaseURLs) && channeltype.ChannelBaseURLs[req.Type] != "" {
			req.BaseURL = channeltype.ChannelBaseURLs[req.Type]
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "预设渠道类型缺失API地址,请手动填写",
			})
			return
		}
	}

	// Parse base URL: strip trailing / and version segments (/v1, /v2, /v3)
	baseURL := strings.TrimRight(req.BaseURL, "/")
	for _, ver := range []string{"/v1", "/v2", "/v3"} {
		baseURL = strings.TrimSuffix(baseURL, ver)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Helper: try one URL, return parsed models or nil+error
	tryURL := func(modelURL string) ([]string, error) {
		httpReq, err := http.NewRequest(http.MethodGet, modelURL, nil)
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %v", err)
		}
		if req.Key != "" {
			key := strings.TrimSpace(strings.Split(req.Key, "\n")[0])
			if !strings.HasPrefix(key, "Bearer ") {
				httpReq.Header.Set("Authorization", "Bearer "+key)
			} else {
				httpReq.Header.Set("Authorization", key)
			}
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("请求目标接口失败: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("读取响应失败: %v", err)
		}

		// Try OpenAI-compatible format: { "object": "list", "data": [{ "id": "..." }, ...] }
		var modelResp struct {
			Object string `json:"object"`
			Data   []struct {
				Id      string `json:"id"`
				Object  string `json:"object"`
				OwnedBy string `json:"owned_by"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &modelResp); err == nil && modelResp.Object == "list" {
			if len(modelResp.Data) > 0 {
				models := make([]string, len(modelResp.Data))
				for i, m := range modelResp.Data {
					models[i] = m.Id
				}
				return models, nil
			}
			// Format matches but list is empty - API reachable
			return nil, fmt.Errorf("接口返回空模型列表")
		}

		// Fallback: { "models": [{ "id": "..." }, ...] }
		var altResp struct {
			Models []struct {
				Id   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &altResp); err == nil && len(altResp.Models) > 0 {
			models := make([]string, len(altResp.Models))
			for i, m := range altResp.Models {
				name := m.Id
				if name == "" {
					name = m.Name
				}
				models[i] = name
			}
			return models, nil
		}

		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Try /v1/models first (standard), fall back to /models, then /v2/models
	models, err := tryURL(baseURL + "/v1/models")
	if err != nil && strings.HasPrefix(err.Error(), "HTTP ") {
		models, err = tryURL(baseURL + "/models")
	}
	if err != nil && strings.HasPrefix(err.Error(), "HTTP ") {
		models, err = tryURL(baseURL + "/v2/models")
	}

	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "获取模型列表失败 (" + err.Error() + "),请确认API地址和密钥是否正确",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
}

func FetchChannelModelsByID(c *gin.Context) {
	channelIdStr := c.Param("id")
	channelId, err := strconv.ParseInt(channelIdStr, 10, 64)
	if err != nil || channelId <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的渠道ID",
		})
		return
	}

	// Read full channel from DB (selectAll=true to get the key)
	channel, err := model.GetChannelById(int(channelId), true)
	if err != nil || channel == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "渠道不存在",
		})
		return
	}

	// Build base URL
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		if channel.Type >= 0 && channel.Type < len(channeltype.ChannelBaseURLs) && channeltype.ChannelBaseURLs[channel.Type] != "" {
			baseURL = channeltype.ChannelBaseURLs[channel.Type]
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "预设渠道类型缺失API地址，请手动填写",
			})
			return
		}
	}

	// Parse base URL: strip trailing / and version segments
	baseURL = strings.TrimRight(baseURL, "/")
	for _, ver := range []string{"/v1", "/v2", "/v3"} {
		baseURL = strings.TrimSuffix(baseURL, ver)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Get the key (already complete from DB)
	key := ""
	if channel.Key != "" {
		key = strings.TrimSpace(strings.Split(channel.Key, "\n")[0])
	}

	// Use the shared fetch logic via a helper request
	// We call ourselves by constructing a request - but since we're in the same
	// process, let's just inline the fetch logic directly
	tryURL := func(modelURL string) ([]string, error) {
		httpReq, err := http.NewRequest(http.MethodGet, modelURL, nil)
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %v", err)
		}
		if key != "" {
			if !strings.HasPrefix(key, "Bearer ") {
				httpReq.Header.Set("Authorization", "Bearer "+key)
			} else {
				httpReq.Header.Set("Authorization", key)
			}
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("请求目标接口失败: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("读取响应失败: %v", err)
		}

		var modelResp struct {
			Object string `json:"object"`
			Data   []struct {
				Id      string `json:"id"`
				Object  string `json:"object"`
				OwnedBy string `json:"owned_by"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &modelResp); err == nil && modelResp.Object == "list" {
			if len(modelResp.Data) > 0 {
				models := make([]string, len(modelResp.Data))
				for i, m := range modelResp.Data {
					models[i] = m.Id
				}
				return models, nil
			}
			return nil, fmt.Errorf("接口返回空模型列表")
		}

		var altResp struct {
			Models []struct {
				Id   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &altResp); err == nil && len(altResp.Models) > 0 {
			models := make([]string, len(altResp.Models))
			for i, m := range altResp.Models {
				name := m.Id
				if name == "" {
					name = m.Name
				}
				models[i] = name
			}
			return models, nil
		}

		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	models, err := tryURL(baseURL + "/v1/models")
	if err != nil && strings.HasPrefix(err.Error(), "HTTP ") {
		models, err = tryURL(baseURL + "/models")
	}
	if err != nil && strings.HasPrefix(err.Error(), "HTTP ") {
		models, err = tryURL(baseURL + "/v2/models")
	}

	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "获取模型列表失败 (" + err.Error() + "),请确认API地址和密钥是否正确",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
}

func UpdateChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	// key 为空时保留原 key（列表接口返回的 key 已脱敏，agent 工具全量更新时不能覆盖清空）
	if channel.Key == "" && channel.Id > 0 {
		if old, oldErr := model.GetChannelById(channel.Id, true); oldErr == nil {
			channel.Key = old.Key
		}
	}
	err = channel.Update()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

// ChannelHealthResponse 健康度指标响应
type ChannelHealthResponse struct {
	ChannelId   int     `json:"channel_id"`
	Degraded    bool    `json:"degraded"`           // 是否熔断中
	HealthScore float64 `json:"health_score"`        // 健康度 0.0~1.0
	SuccessRate float64 `json:"success_rate"`         // 成功率 0.0~1.0
	TokPerSec   float64 `json:"tok_per_sec"`         // 平均吞吐量 tok/s
	AvgTTFT     int64   `json:"avg_ttft_ms"`         // 平均首次响应时间 ms
	RecordCount int     `json:"record_count"`        // 窗口内记录数
}

// GetChannelHealth 返回所有渠道的健康度指标
func GetChannelHealth(c *gin.Context) {
	channels, err := model.GetAllChannels(0, 0, "all", "", "")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	healthData := make([]ChannelHealthResponse, 0, len(channels))
	for _, ch := range channels {
		resp := ChannelHealthResponse{
			ChannelId:   ch.Id,
			Degraded:    monitor.GlobalPerformanceStore.IsDegraded(ch.Id),
			HealthScore: monitor.GlobalPerformanceStore.GetHealthScore(ch.Id),
			SuccessRate: monitor.GlobalPerformanceStore.GetRecentSuccessRate(ch.Id),
			TokPerSec:   monitor.GlobalPerformanceStore.GetChannelSpeed(ch.Id),
			AvgTTFT:     monitor.GlobalPerformanceStore.GetRecentTTFT(ch.Id),
		}
		// 有数据时记录数设为窗口大小（简化处理）
		count := monitor.GlobalPerformanceStore.GetRecordCount(ch.Id)
		if count > 0 {
			resp.RecordCount = count
		}
		healthData = append(healthData, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    healthData,
	})
}

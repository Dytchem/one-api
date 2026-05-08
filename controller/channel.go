package controller

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"
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
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请求参数错误: " + err.Error(),
		})
		return
	}

	if req.BaseURL == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "渠道API地址不能为空",
		})
		return
	}

	// Build request to the channel's /v1/models endpoint
	modelURL := strings.TrimRight(req.BaseURL, "/") + "/v1/models"
	httpReq, err := http.NewRequest(http.MethodGet, modelURL, nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "创建请求失败: " + err.Error(),
		})
		return
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
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请求目标接口失败: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "读取响应失败: " + err.Error(),
		})
		return
	}

	// Try to parse OpenAI-compatible /v1/models response first
	var modelResp struct {
		Object string `json:"object"`
		Data   []struct {
			Id      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelResp); err == nil && len(modelResp.Data) > 0 {
		models := make([]string, len(modelResp.Data))
		for i, m := range modelResp.Data {
			models[i] = m.Id
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    models,
		})
		return
	}

	// Fallback: try common alternative formats
	// Some APIs return { "models": [...] }
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
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    models,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "未能解析目标接口返回的模型列表，请确认API地址是否正确（HTTP " + strconv.Itoa(resp.StatusCode) + "）",
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

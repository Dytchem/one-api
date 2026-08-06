package model

import (
	"encoding/json"
	"fmt"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"gorm.io/gorm"
)

const (
	ChannelStatusUnknown          = 0
	ChannelStatusEnabled          = 1 // don't use 0, 0 is the default value!
	ChannelStatusManuallyDisabled = 2 // also don't use 0
	ChannelStatusAutoDisabled     = 3
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"type:text"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              *string `json:"other"`   // DEPRECATED: please save config to field Config
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(32);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:varchar(1024);default:''"`
	Priority           *int64  `json:"priority" gorm:"bigint;default:0"`
	Config             string  `json:"config"`
	SystemPrompt       *string `json:"system_prompt" gorm:"type:text"`
}

type ChannelConfig struct {
	Region            string `json:"region,omitempty"`
	SK                string `json:"sk,omitempty"`
	AK                string `json:"ak,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	APIVersion        string `json:"api_version,omitempty"`
	LibraryID         string `json:"library_id,omitempty"`
	Plugin            string `json:"plugin,omitempty"`
	VertexAIProjectID string `json:"vertex_ai_project_id,omitempty"`
	VertexAIADC       string `json:"vertex_ai_adc,omitempty"`
}

// GetAllChannels 支持白名单排序：order 必须是 channel 表的列名，sort 只能是 asc/desc
func GetAllChannels(startIdx int, num int, scope string, order string, sort string) ([]*Channel, error) {
	var channels []*Channel
	var err error
	orderClause := "id desc"
	if order != "" {
		// 白名单校验，防止 SQL 注入
		switch order {
		case "id", "name", "type", "models", "status", "balance", "used_quota",
			"priority", "created_time", "balance_updated_time", "group":
			if sort == "asc" || sort == "desc" {
				orderClause = order + " " + sort
			}
		}
	} else if sort == "asc" {
		orderClause = "id asc"
	}
	switch scope {
	case "all":
		err = DB.Order(orderClause).Find(&channels).Error
	case "disabled":
		err = DB.Order(orderClause).Where("status = ? or status = ?", ChannelStatusAutoDisabled, ChannelStatusManuallyDisabled).Find(&channels).Error
	default:
		err = DB.Order(orderClause).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

func SearchChannels(keyword string) (channels []*Channel, err error) {
	err = DB.Omit("key").Where("id = ? or name LIKE ?", helper.String2Int(keyword), keyword+"%").Find(&channels).Error
	return channels, err
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(&channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(&channel, "id = ?", id).Error
	}
	return &channel, err
}

func BatchInsertChannels(channels []Channel) error {
	var err error
	err = DB.Create(&channels).Error
	if err != nil {
		return err
	}
	for _, channel_ := range channels {
		err = channel_.AddAbilities()
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	return *channel.BaseURL
}

func (channel *Channel) GetModelMapping() map[string]string {
	if channel.ModelMapping == nil || *channel.ModelMapping == "" || *channel.ModelMapping == "{}" {
		return nil
	}
	modelMapping := make(map[string]string)
	err := json.Unmarshal([]byte(*channel.ModelMapping), &modelMapping)
	if err != nil {
		logger.SysError(fmt.Sprintf("failed to unmarshal model mapping for channel %d, error: %s", channel.Id, err.Error()))
		return nil
	}
	return modelMapping
}

func (channel *Channel) Insert() error {
	var err error
	err = DB.Create(channel).Error
	if err != nil {
		return err
	}
	err = channel.AddAbilities()
	return err
}

func (channel *Channel) Update() error {
	var err error
	// dyt-93: 显式 Select 更新列，使 models/group 等"清空"操作生效（原 Updates(struct) 会跳过零值）。
	// balance/weight/priority 由专用单字段接口管理（status/priority/weight/余额刷新），不在此全量
	// 更新，防止部分调用方把它们清零。key 由 UpdateChannel 空值保留逻辑保护。
	err = DB.Model(channel).Select(
		"type", "key", "status", "name", "base_url", "other",
		"models", "group", "model_mapping", "config", "system_prompt",
	).Updates(channel).Error
	if err != nil {
		return err
	}
	// dyt-93: 回读不返回密钥
	DB.Model(channel).Select("id", "type", "status", "name", "weight", "created_time", "test_time",
		"response_time", "base_url", "other", "balance", "balance_updated_time", "models", "group",
		"used_quota", "model_mapping", "priority", "config", "system_prompt").First(channel, "id = ?", channel.Id)
	err = channel.UpdateAbilities()
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     helper.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		logger.SysError("failed to update response time: " + err.Error())
	}
}

// dyt-93: 单字段更新（行内操作专用，避免部分 PUT 走全量 Select 把其余字段清零）
func UpdateChannelStatus(id int, status int) error {
	// 与 UpdateChannelStatusById 一致：同步 ability 表 + 刷新内存缓存，
	// 否则行内禁用后健康路由仍会选中该渠道
	err := UpdateAbilityStatus(id, status == ChannelStatusEnabled)
	if err != nil {
		logger.SysError("failed to update ability status: " + err.Error())
	}
	err = DB.Model(&Channel{}).Where("id = ?", id).Update("status", status).Error
	if err != nil {
		return err
	}
	if config.MemoryCacheEnabled {
		InitChannelCache()
	}
	return nil
}

func UpdateChannelPriority(id int, priority int64) error {
	// dyt-93: 同步 ability.priority（健康路由按 ability 表 MAX(priority) 选渠道），
	// 行内改优先级立即在分配路径生效
	err := DB.Model(&Ability{}).Where("channel_id = ?", id).Update("priority", priority).Error
	if err != nil {
		logger.SysError("failed to update ability priority: " + err.Error())
	}
	err = DB.Model(&Channel{}).Where("id = ?", id).Update("priority", priority).Error
	if err != nil {
		return err
	}
	if config.MemoryCacheEnabled {
		InitChannelCache()
	}
	return nil
}

func UpdateChannelWeight(id int, weight uint) error {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("weight", weight).Error
	if err != nil {
		return err
	}
	if config.MemoryCacheEnabled {
		InitChannelCache()
	}
	return nil
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: helper.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		logger.SysError("failed to update balance: " + err.Error())
	}
}

func (channel *Channel) Delete() error {
	var err error
	err = DB.Delete(channel).Error
	if err != nil {
		return err
	}
	err = channel.DeleteAbilities()
	return err
}

func (channel *Channel) LoadConfig() (ChannelConfig, error) {
	var cfg ChannelConfig
	if channel.Config == "" {
		return cfg, nil
	}
	err := json.Unmarshal([]byte(channel.Config), &cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func UpdateChannelStatusById(id int, status int) {
	err := UpdateAbilityStatus(id, status == ChannelStatusEnabled)
	if err != nil {
		logger.SysError("failed to update ability status: " + err.Error())
	}
	err = DB.Model(&Channel{}).Where("id = ?", id).Update("status", status).Error
	if err != nil {
		logger.SysError("failed to update channel status: " + err.Error())
	}
	// dyt-93: 立即刷新内存渠道缓存（否则禁用后最长 SyncFrequency 内仍可能被选中）
	if config.MemoryCacheEnabled {
		InitChannelCache()
	}
}

func UpdateChannelUsedQuota(id int, quota int64) {
	if config.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int64) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		logger.SysError("failed to update channel used quota: " + err.Error())
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	result := DB.Where("status = ?", status).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func DeleteDisabledChannel() (int64, error) {
	result := DB.Where("status = ? or status = ?", ChannelStatusAutoDisabled, ChannelStatusManuallyDisabled).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

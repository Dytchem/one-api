package model

import (
	"errors"
	"time"
)

// ChatSession dyt-103: 会话记录（按账号跨设备同步）。
// 会话内容（messages JSON）由前端定期同步，同一账号各设备可见；
// 实时流式事件仍由 pi-bridge 负责，本表是会话列表与历史的唯一权威来源。
type ChatSession struct {
	Id        int    `json:"id" gorm:"primaryKey"`
	UserId    int    `json:"user_id" gorm:"index"`
	Kind      string `json:"kind" gorm:"index"` // chat | agent
	SessionId string `json:"session_id" gorm:"uniqueIndex:idx_user_kind_sid"`
	Title     string `json:"title"`
	Messages  string `json:"messages"` // JSON 字符串（前端序列化；大附件/工具结果已由前端压缩）
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func ListChatSessions(userId int, kind string) ([]*ChatSession, error) {
	var sessions []*ChatSession
	err := DB.Where("user_id = ? and kind = ?", userId, kind).
		Order("updated_at desc").Limit(100).Find(&sessions).Error
	return sessions, err
}

// UpsertChatSession 按 (user_id, kind, session_id) 幂等更新标题与内容
func UpsertChatSession(s *ChatSession) error {
	if s.UserId == 0 || s.Kind == "" || s.SessionId == "" {
		return errors.New("user_id/kind/session_id 不能为空")
	}
	now := time.Now().Unix()
	var existing ChatSession
	err := DB.Where("user_id = ? and kind = ? and session_id = ?", s.UserId, s.Kind, s.SessionId).
		First(&existing).Error
	if err != nil {
		// 不存在：创建
		s.CreatedAt = now
		s.UpdatedAt = now
		return DB.Create(s).Error
	}
	// 存在：更新（标题/内容/时间）
	existing.Title = s.Title
	if s.Messages != "" {
		existing.Messages = s.Messages
	}
	existing.UpdatedAt = now
	return DB.Model(&existing).Select("title", "messages", "updated_at").Updates(existing).Error
}

func DeleteChatSession(userId int, kind string, sessionId string) error {
	return DB.Where("user_id = ? and kind = ? and session_id = ?", userId, kind, sessionId).
		Delete(&ChatSession{}).Error
}

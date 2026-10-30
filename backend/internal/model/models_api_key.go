package model

import (
	"time"

	"gorm.io/gorm"
)

const (
	// UserAPIKeySourceManual 是用户在设置页手动创建的 Key。
	UserAPIKeySourceManual = "manual"
	// UserAPIKeySourceLogin 预留给外部客户端经浏览器授权登录后签发的 Key。
	UserAPIKeySourceLogin = "login"
)

// UserAPIKey 是用户在站外工具里调用平台开放接口的凭证。
// 只保存 SHA-256 哈希；明文只在创建时返回一次，之后只能展示前后几位。
type UserAPIKey struct {
	ID         string         `json:"id" gorm:"primaryKey;size:36"`
	UserID     string         `json:"userId" gorm:"size:36;not null;index"`
	Name       string         `json:"name" gorm:"size:80;not null"`
	KeyPrefix  string         `json:"keyPrefix" gorm:"size:16;not null"`
	KeyLast4   string         `json:"keyLast4" gorm:"size:8;not null"`
	KeyHash    string         `json:"-" gorm:"size:64;not null;uniqueIndex"`
	Source     string         `json:"source" gorm:"size:16;not null;default:manual"`
	ExpiresAt  *time.Time     `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time     `json:"lastUsedAt,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index"`
}

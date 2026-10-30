package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"yingce/backend/internal/model"
)

// ErrUserAPIKeyLimit 表示用户的有效 Key 已达到上限。
var ErrUserAPIKeyLimit = errors.New("user api key limit reached")

// CreateUserAPIKey 在同一事务里检查数量上限并写入。PostgreSQL 先锁住用户行，
// 把同一用户的并发创建串行化，避免两个事务都读到旧数量而一起越过上限。
func (r *Repository) CreateUserAPIKey(key *model.UserAPIKey, limit int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Select("id").Where("id = ?", key.UserID)
		if r.Dialect() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var owner model.User
		if err := query.First(&owner).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.UserAPIKey{}).Where("user_id = ?", key.UserID).Count(&count).Error; err != nil {
			return err
		}
		if count >= limit {
			return ErrUserAPIKeyLimit
		}
		return tx.Create(key).Error
	})
}

func (r *Repository) UserAPIKeys(userID string) ([]model.UserAPIKey, error) {
	var keys []model.UserAPIKey
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&keys).Error
	return keys, err
}

func (r *Repository) UserAPIKeyByHash(hash string) (*model.UserAPIKey, error) {
	var key model.UserAPIKey
	if err := r.db.Where("key_hash = ?", hash).First(&key).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

// DeleteUserAPIKey 软删除用户自己的 Key；返回是否命中，用于区分「不存在」与「已删除」。
func (r *Repository) DeleteUserAPIKey(userID string, id string) (bool, error) {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.UserAPIKey{})
	return result.RowsAffected > 0, result.Error
}

// TouchUserAPIKey 只在距上次记录超过 interval 时更新最后使用时间，避免每个请求都写库。
func (r *Repository) TouchUserAPIKey(id string, at time.Time, interval time.Duration) error {
	return r.db.Model(&model.UserAPIKey{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", id, at.Add(-interval)).
		UpdateColumn("last_used_at", at).Error
}

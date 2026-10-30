// 平台 API Key：用户在站外工具里调用开放接口时用它代替登录 Cookie。
// 安全边界：库里只存哈希，明文只在创建时返回一次；认 Key 后按账号状态再校验一次，禁用账号的 Key 立即失效。

package app

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	// UserAPIKeyPrefix 让用户和日志扫描工具一眼认出这是平台 Key。
	UserAPIKeyPrefix = "sk-"
	// MaxUserAPIKeys 是每个用户同时有效的 Key 上限。
	MaxUserAPIKeys          = 10
	maxUserAPIKeyNameRunes  = 40
	userAPIKeyTouchInterval = time.Minute
)

type CreateUserAPIKeyRequest struct {
	Name string `json:"name"`
}

// CreatedUserAPIKey 是创建结果；Secret 只在这一次响应里出现。
type CreatedUserAPIKey struct {
	Key    model.UserAPIKey `json:"key"`
	Secret string           `json:"secret"`
}

func (s *Service) CreateUserAPIKey(actor *model.User, req CreateUserAPIKeyRequest) (*CreatedUserAPIKey, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, BadAuthRequest("请填写 Key 名称")
	}
	if utf8.RuneCountInString(name) > maxUserAPIKeyNameRunes {
		return nil, BadAuthRequest(fmt.Sprintf("Key 名称不能超过 %d 个字", maxUserAPIKeyNameRunes))
	}
	secret := UserAPIKeyPrefix + auth.RandomToken()
	now := time.Now().UTC()
	key := model.UserAPIKey{
		ID:        newID(),
		UserID:    actor.ID,
		Name:      name,
		KeyPrefix: secret[:len(UserAPIKeyPrefix)+4],
		KeyLast4:  secret[len(secret)-4:],
		KeyHash:   auth.HashToken(secret),
		Source:    model.UserAPIKeySourceManual,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateUserAPIKey(&key, MaxUserAPIKeys); err != nil {
		if errors.Is(err, repository.ErrUserAPIKeyLimit) {
			return nil, BadAuthRequest(fmt.Sprintf("最多只能创建 %d 个 Key，请先删除不用的", MaxUserAPIKeys))
		}
		return nil, fmt.Errorf("保存 API Key 失败：%w", err)
	}
	return &CreatedUserAPIKey{Key: key, Secret: secret}, nil
}

func (s *Service) UserAPIKeys(actor *model.User) ([]model.UserAPIKey, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	keys, err := s.repo.UserAPIKeys(actor.ID)
	if err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []model.UserAPIKey{}
	}
	return keys, nil
}

func (s *Service) DeleteUserAPIKey(actor *model.User, id string) error {
	if actor == nil {
		return Unauthorized("请先登录")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return BadAuthRequest("缺少 Key ID")
	}
	deleted, err := s.repo.DeleteUserAPIKey(actor.ID, id)
	if err != nil {
		return err
	}
	if !deleted {
		return NotFound("Key 不存在或已删除")
	}
	return nil
}

// AuthenticateUserAPIKey 把请求头里的 Key 换成用户。Key 不存在、已删除、已过期都返回同一个 401 文案；
// 只有 Key 有效但账号被禁用时返回 403，与登录态的禁用提示一致。
func (s *Service) AuthenticateUserAPIKey(secret string) (*model.User, error) {
	secret = strings.TrimSpace(secret)
	if !strings.HasPrefix(secret, UserAPIKeyPrefix) || len(secret) <= len(UserAPIKeyPrefix) {
		return nil, Unauthorized("API Key 无效")
	}
	key, err := s.repo.UserAPIKeyByHash(auth.HashToken(secret))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, Unauthorized("API Key 无效")
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return nil, Unauthorized("API Key 无效")
	}
	user, err := s.repo.User(key.UserID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, Unauthorized("API Key 无效")
	}
	if err != nil {
		return nil, err
	}
	if user.Status != model.UserStatusActive {
		return nil, Forbidden("该账号已被禁用")
	}
	// 最后使用时间只用于展示：一分钟内已记录过就不再写库，写失败也不影响本次调用。
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= userAPIKeyTouchInterval {
		_ = s.repo.TouchUserAPIKey(key.ID, now, userAPIKeyTouchInterval)
	}
	return user, nil
}

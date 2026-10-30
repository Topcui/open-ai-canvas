package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newUserAPIKeyTestService(t *testing.T) (*Service, *gorm.DB, *model.User, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-keys.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserAPIKey{}); err != nil {
		t.Fatal(err)
	}
	owner := &model.User{ID: "owner", Username: "owner", Role: model.UserRoleUser, Status: model.UserStatusActive}
	other := &model.User{ID: "other", Username: "other", Role: model.UserRoleUser, Status: model.UserStatusActive}
	for _, user := range []*model.User{owner, other} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &Service{repo: repository.New(db)}, db, owner, other
}

func appErrorStatus(err error) int {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	return 0
}

func TestUserAPIKeyLifecycle(t *testing.T) {
	svc, db, owner, other := newUserAPIKeyTestService(t)
	created, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: "  公司电脑  "})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Secret, UserAPIKeyPrefix) || len(created.Secret) != len(UserAPIKeyPrefix)+64 {
		t.Fatalf("secret format = %q", created.Secret)
	}
	if created.Key.Name != "公司电脑" || created.Key.Source != model.UserAPIKeySourceManual {
		t.Fatalf("created key = %+v", created.Key)
	}
	if created.Key.KeyPrefix != created.Secret[:7] || created.Key.KeyLast4 != created.Secret[len(created.Secret)-4:] {
		t.Fatalf("display parts = %q %q", created.Key.KeyPrefix, created.Key.KeyLast4)
	}
	var stored model.UserAPIKey
	if err := db.First(&stored, "id = ?", created.Key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.KeyHash != auth.HashToken(created.Secret) || strings.Contains(stored.KeyHash, created.Secret[len(UserAPIKeyPrefix):]) {
		t.Fatal("only the hash of the secret may be stored")
	}

	user, err := svc.AuthenticateUserAPIKey(created.Secret)
	if err != nil || user.ID != owner.ID {
		t.Fatalf("authenticate = %+v, %v", user, err)
	}
	if err := db.First(&stored, "id = ?", created.Key.ID).Error; err != nil || stored.LastUsedAt == nil {
		t.Fatalf("last used time not recorded: %+v %v", stored.LastUsedAt, err)
	}

	keys, err := svc.UserAPIKeys(owner)
	if err != nil || len(keys) != 1 || keys[0].KeyHash == "" {
		t.Fatalf("owner keys = %+v, %v", keys, err)
	}
	if keys, err := svc.UserAPIKeys(other); err != nil || len(keys) != 0 {
		t.Fatalf("other user must not see owner keys: %+v, %v", keys, err)
	}
	if err := svc.DeleteUserAPIKey(other, created.Key.ID); appErrorStatus(err) != 404 {
		t.Fatalf("deleting another user's key = %v", err)
	}
	if err := svc.DeleteUserAPIKey(owner, created.Key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateUserAPIKey(created.Secret); appErrorStatus(err) != 401 {
		t.Fatalf("deleted key must stop working, got %v", err)
	}
	if err := svc.DeleteUserAPIKey(owner, created.Key.ID); appErrorStatus(err) != 404 {
		t.Fatalf("deleting twice = %v", err)
	}
}

func TestUserAPIKeyNameAndLimit(t *testing.T) {
	svc, _, owner, other := newUserAPIKeyTestService(t)
	for _, name := range []string{"", "   ", strings.Repeat("名", maxUserAPIKeyNameRunes+1)} {
		if _, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: name}); appErrorStatus(err) != 400 {
			t.Fatalf("name %q = %v", name, err)
		}
	}
	if _, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: strings.Repeat("名", maxUserAPIKeyNameRunes)}); err != nil {
		t.Fatalf("name at limit should be accepted: %v", err)
	}
	for index := 1; index < MaxUserAPIKeys; index++ {
		if _, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: "key"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: "one too many"}); appErrorStatus(err) != 400 {
		t.Fatalf("limit not enforced: %v", err)
	}
	if _, err := svc.CreateUserAPIKey(other, CreateUserAPIKeyRequest{Name: "other"}); err != nil {
		t.Fatalf("limit must be per user: %v", err)
	}
	if _, err := svc.CreateUserAPIKey(nil, CreateUserAPIKeyRequest{Name: "anonymous"}); appErrorStatus(err) != 401 {
		t.Fatalf("anonymous create = %v", err)
	}
}

func TestAuthenticateUserAPIKeyRejectsInvalidExpiredAndDisabled(t *testing.T) {
	svc, db, owner, _ := newUserAPIKeyTestService(t)
	for _, secret := range []string{"", "sk-", "not-a-platform-key", UserAPIKeyPrefix + strings.Repeat("0", 64)} {
		if _, err := svc.AuthenticateUserAPIKey(secret); appErrorStatus(err) != 401 {
			t.Fatalf("secret %q = %v", secret, err)
		}
	}
	expired, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: "expired"})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.Model(&model.UserAPIKey{}).Where("id = ?", expired.Key.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateUserAPIKey(expired.Secret); appErrorStatus(err) != 401 {
		t.Fatalf("expired key = %v", err)
	}
	active, err := svc.CreateUserAPIKey(owner, CreateUserAPIKeyRequest{Name: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", owner.ID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateUserAPIKey(active.Secret); appErrorStatus(err) != 403 {
		t.Fatalf("disabled account = %v", err)
	}
}

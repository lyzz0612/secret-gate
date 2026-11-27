package app

import (
	"context"
	"errors"

	"github.com/lyzz0612/deploy-center/internal/domain"
)

// 错误哨兵：httpapi 据此映射 HTTP 状态码。
var (
	ErrInvalid     = errors.New("参数不合法")
	ErrNotFound    = errors.New("不存在")
	ErrConflict    = errors.New("冲突")
	ErrKeyMismatch = errors.New("主密钥与数据库不匹配")
)

// Sealer 密文封存端口（infra/crypto 实现）。
type Sealer interface {
	Seal(plaintext, aad []byte) (ciphertext, nonce []byte, err error)
	Open(ciphertext, nonce, aad []byte) ([]byte, error)
}

// Repo 持久化端口（infra/sqlite 实现）。
type Repo interface {
	GetMeta(ctx context.Context, key string) (string, bool, error)
	SetMeta(ctx context.Context, key, value string) error

	ListGroups(ctx context.Context) ([]domain.SecretGroup, error)
	CreateGroup(ctx context.Context, path, name, now string) (domain.SecretGroup, error)
	RenameGroup(ctx context.Context, id int64, name string) error
	DeleteGroup(ctx context.Context, id int64) error

	ListSecrets(ctx context.Context, groupID int64) ([]domain.SecretMeta, error)
	PutSecret(ctx context.Context, groupID int64, key string, ciphertext, nonce, aadSalt []byte, now string) error
	GetSecret(ctx context.Context, groupID int64, key string) (domain.SecretBlob, error)
	DeleteSecret(ctx context.Context, groupID int64, key string) error
	SecretByID(ctx context.Context, id int64) (*domain.SecretBlob, error)

	Overview(ctx context.Context) (OverviewCounts, error)

	// providers（实例）
	ListProviders(ctx context.Context) ([]domain.ProviderInstance, error)
	ProviderByID(ctx context.Context, id int64) (*domain.ProviderInstance, error)
	CreateProvider(ctx context.Context, typ, name, configJSON, rulesJSON, now string) (domain.ProviderInstance, error)
	UpdateProvider(ctx context.Context, id int64, name, configJSON, rulesJSON string, enabled bool, now string) error
	DeleteProvider(ctx context.Context, id int64) error

	// users
	ListUsers(ctx context.Context) ([]domain.User, error)
	UserByID(ctx context.Context, id int64) (*domain.User, error)
	UserByKey(ctx context.Context, typ, extID string) (*domain.User, error)
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	TouchUser(ctx context.Context, id int64, now string) error
	DeleteUser(ctx context.Context, id int64) error

	// user groups
	ListUserGroups(ctx context.Context) ([]domain.UserGroup, error)
	UserGroupByID(ctx context.Context, id int64) (*domain.UserGroup, error)
	CreateUserGroup(ctx context.Context, name, description, now string) (domain.UserGroup, error)
	DeleteUserGroup(ctx context.Context, id int64) error
	SetGroupMembers(ctx context.Context, groupID int64, userIDs []int64) error
	GroupMembers(ctx context.Context, groupID int64) ([]domain.User, error)
	UserGroupIDs(ctx context.Context, userID int64) ([]int64, error)

	// grants
	ListGrants(ctx context.Context, subjectKind string, subjectID int64) ([]domain.Grant, error)
	GrantsForSubjects(ctx context.Context, userID int64, groupIDs []int64) ([]domain.Grant, error)
	CreateGrant(ctx context.Context, g domain.Grant, now string) (domain.Grant, error)
	DeleteGrant(ctx context.Context, id int64) error
}

// OverviewCounts 总览页计数。
type OverviewCounts struct {
	Groups    int64 `json:"groups"`
	Secrets   int64 `json:"secrets"`
	Users     int64 `json:"users"`
	Providers int64 `json:"providers"`
}

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

	Overview(ctx context.Context) (OverviewCounts, error)
}

// OverviewCounts 总览页计数。
type OverviewCounts struct {
	Groups    int64 `json:"groups"`
	Secrets   int64 `json:"secrets"`
	Users     int64 `json:"users"`
	Pending   int64 `json:"pending"`
	Providers int64 `json:"providers"`
}

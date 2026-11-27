package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/lyzz0612/deploy-center/internal/domain"
)

// MaxSecretValueLen 单条密钥值上限。
const MaxSecretValueLen = 64 * 1024

// SecretService 密钥与分组的用例编排：加密入库、按需解密、真删。
type SecretService struct {
	repo Repo
	seal Sealer
	now  func() time.Time
}

func NewSecretService(repo Repo, seal Sealer) *SecretService {
	return &SecretService{repo: repo, seal: seal, now: time.Now}
}

// WithClock 注入时钟（测试用）。
func (s *SecretService) WithClock(now func() time.Time) *SecretService {
	s.now = now
	return s
}

func (s *SecretService) ts() string { return s.now().UTC().Format(time.RFC3339) }

// ---------- 分组 ----------

func (s *SecretService) ListGroups(ctx context.Context) ([]domain.SecretGroup, error) {
	return s.repo.ListGroups(ctx)
}

// CreateGroup 校验两级 path；name 缺省取 path 最后一段。
func (s *SecretService) CreateGroup(ctx context.Context, path, name string) (domain.SecretGroup, error) {
	if err := domain.ValidGroupPath(path); err != nil {
		return domain.SecretGroup{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if name == "" {
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			name = path[i+1:]
		} else {
			name = path
		}
	}
	if err := domain.ValidDisplayName(name); err != nil {
		return domain.SecretGroup{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.repo.CreateGroup(ctx, path, name, s.ts())
}

func (s *SecretService) RenameGroup(ctx context.Context, id int64, name string) error {
	if err := domain.ValidDisplayName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.repo.RenameGroup(ctx, id, name)
}

func (s *SecretService) DeleteGroup(ctx context.Context, id int64) error {
	return s.repo.DeleteGroup(ctx, id)
}

// ---------- 密钥 ----------

func (s *SecretService) ListSecrets(ctx context.Context, groupID int64) ([]domain.SecretMeta, error) {
	return s.repo.ListSecrets(ctx, groupID)
}

// PutSecret 新增或覆盖一条密钥：每次写入都换新 salt + 新 nonce（同一值两次保存密文也不同）。
func (s *SecretService) PutSecret(ctx context.Context, groupID int64, key, value string) error {
	if err := domain.ValidSecretKey(key); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if value == "" {
		return fmt.Errorf("%w: 密钥值不能为空", ErrInvalid)
	}
	if len(value) > MaxSecretValueLen {
		return fmt.Errorf("%w: 密钥值超过 %d 字节", ErrInvalid, MaxSecretValueLen)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("生成 salt: %w", err)
	}
	aad := []byte("secret:" + hex.EncodeToString(salt))
	ct, nonce, err := s.seal.Seal([]byte(value), aad)
	if err != nil {
		return fmt.Errorf("加密: %w", err)
	}
	return s.repo.PutSecret(ctx, groupID, key, ct, nonce, salt, s.ts())
}

// RevealSecret 解密一条密钥。列表接口永远不带明文，明文只从这里出。
func (s *SecretService) RevealSecret(ctx context.Context, groupID int64, key string) (string, error) {
	b, err := s.repo.GetSecret(ctx, groupID, key)
	if err != nil {
		return "", err
	}
	aad := []byte("secret:" + hex.EncodeToString(b.AADSalt))
	plain, err := s.seal.Open(b.Ciphertext, b.Nonce, aad)
	if err != nil {
		return "", fmt.Errorf("解密: %w", err)
	}
	return string(plain), nil
}

func (s *SecretService) DeleteSecret(ctx context.Context, groupID int64, key string) error {
	return s.repo.DeleteSecret(ctx, groupID, key)
}

func (s *SecretService) Overview(ctx context.Context) (OverviewCounts, error) {
	return s.repo.Overview(ctx)
}

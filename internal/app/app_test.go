package app_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/infra/crypto"
	"github.com/lyzz0612/deploy-center/internal/infra/sqlite"
)

const testMaster = "test-master-key-32-bytes-abcdefg" // 32 字节

func newStack(t *testing.T) (app.Repo, *crypto.AEAD, *app.SecretService) {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	sealer, err := crypto.New([]byte(testMaster))
	if err != nil {
		t.Fatal(err)
	}
	return repo, sealer, app.NewSecretService(repo, sealer)
}

func TestSentinel(t *testing.T) {
	t.Run("首次写入", func(t *testing.T) {
		repo, seal, _ := newStack(t)
		if err := app.EnsureSentinel(repo, seal); err != nil {
			t.Fatalf("首次应写入哨兵: %v", err)
		}
		// 再验一次：应通过
		if err := app.EnsureSentinel(repo, seal); err != nil {
			t.Fatalf("同密钥二次校验应通过: %v", err)
		}
	})

	t.Run("换主密钥被拦截", func(t *testing.T) {
		repo, seal, _ := newStack(t)
		if err := app.EnsureSentinel(repo, seal); err != nil {
			t.Fatal(err)
		}
		wrong, err := crypto.New([]byte("another-master-key-32-bytes-xxxx"))
		if err != nil {
			t.Fatal(err)
		}
		if err := app.EnsureSentinel(repo, wrong); !errors.Is(err, app.ErrKeyMismatch) {
			t.Fatalf("换 MASTER_KEY 应报 ErrKeyMismatch, got %v", err)
		}
	})
}

func TestSecretService_Roundtrip(t *testing.T) {
	ctx := t.Context()
	_, _, svc := newStack(t)

	g, err := svc.CreateGroup(ctx, "minigames/prod", "")
	if err != nil {
		t.Fatal(err)
	}
	val := "AKIAIOSFODNN7EXAMPLE  值含中文与空格"
	if err := svc.PutSecret(ctx, g.ID, "OSS_ACCESS_KEY", val); err != nil {
		t.Fatal(err)
	}
	got, err := svc.RevealSecret(ctx, g.ID, "OSS_ACCESS_KEY")
	if err != nil || got != val {
		t.Fatalf("reveal = %q err=%v", got, err)
	}

	// 改值
	if err := svc.PutSecret(ctx, g.ID, "OSS_ACCESS_KEY", "NEW"); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.RevealSecret(ctx, g.ID, "OSS_ACCESS_KEY")
	if got != "NEW" {
		t.Errorf("更新后 = %q", got)
	}

	// 改分组名后仍能解密（AAD 用随机 salt 而不是 path 的设计点）
	if err := svc.RenameGroup(ctx, g.ID, "新名字"); err != nil {
		t.Fatal(err)
	}
	got, err = svc.RevealSecret(ctx, g.ID, "OSS_ACCESS_KEY")
	if err != nil || got != "NEW" {
		t.Errorf("改名后 reveal = %q err=%v（AAD 不应受改名影响）", got, err)
	}

	// 列表不含值（元信息只有 id/key/时间）
	ms, _ := svc.ListSecrets(ctx, g.ID)
	if len(ms) != 1 || ms[0].Key != "OSS_ACCESS_KEY" {
		t.Errorf("列表 = %+v", ms)
	}

	// 删除后 reveal 404
	if err := svc.DeleteSecret(ctx, g.ID, "OSS_ACCESS_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevealSecret(ctx, g.ID, "OSS_ACCESS_KEY"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("删除后应 404, got %v", err)
	}
}

func TestSecretService_Validation(t *testing.T) {
	ctx := t.Context()
	_, _, svc := newStack(t)
	g, _ := svc.CreateGroup(ctx, "a", "a")

	for _, k := range []string{"", "1A", "A-B", "A B"} {
		if err := svc.PutSecret(ctx, g.ID, k, "v"); !errors.Is(err, app.ErrInvalid) {
			t.Errorf("key %q 应报 ErrInvalid, got %v", k, err)
		}
	}
	if err := svc.PutSecret(ctx, g.ID, "K", ""); !errors.Is(err, app.ErrInvalid) {
		t.Errorf("空值应报 ErrInvalid, got %v", err)
	}
	long := make([]byte, app.MaxSecretValueLen+1)
	for i := range long {
		long[i] = 'x'
	}
	if err := svc.PutSecret(ctx, g.ID, "K", string(long)); !errors.Is(err, app.ErrInvalid) {
		t.Errorf("超长值应报 ErrInvalid, got %v", err)
	}

	for _, p := range []string{"", "A", "a/b/c", "a//b"} {
		if _, err := svc.CreateGroup(ctx, p, ""); !errors.Is(err, app.ErrInvalid) {
			t.Errorf("path %q 应报 ErrInvalid, got %v", p, err)
		}
	}
	if _, err := svc.CreateGroup(ctx, "a", ""); !errors.Is(err, app.ErrConflict) {
		t.Errorf("重复 path 应报 ErrConflict, got %v", err)
	}
}

func TestSecretService_SameValueDifferentCiphertext(t *testing.T) {
	ctx := t.Context()
	repo, _, svc := newStack(t)
	g, _ := svc.CreateGroup(ctx, "a", "a")
	svc.PutSecret(ctx, g.ID, "K", "same-value")
	b1, _ := repo.GetSecret(ctx, g.ID, "K")
	svc.PutSecret(ctx, g.ID, "K", "same-value")
	b2, _ := repo.GetSecret(ctx, g.ID, "K")
	if string(b1.Ciphertext) == string(b2.Ciphertext) || string(b1.AADSalt) == string(b2.AADSalt) {
		t.Error("同值两次保存应产生不同密文与 salt")
	}
}

func TestLoginService(t *testing.T) {
	l := app.NewLoginService("correct-password")

	if !l.Check("correct-password") {
		t.Error("正确口令应通过")
	}
	if l.Check("wrong") || l.Check("") {
		t.Error("错误口令应拒绝")
	}

	now := time.Now()
	tok, _ := l.NewSession(now)
	if !l.VerifySession(tok, now.Add(time.Hour)) {
		t.Error("会话应在有效期内通过")
	}
	if l.VerifySession(tok, now.Add(app.SessionTTL+time.Minute)) {
		t.Error("过期会话应拒绝")
	}
	if l.VerifySession("v1.9999999999.deadbeef", now) {
		t.Error("伪造签名应拒绝")
	}
	if l.VerifySession("garbage", now) {
		t.Error("垃圾 token 应拒绝")
	}

	// 改口令后旧会话全部失效
	l2 := app.NewLoginService("changed")
	if l2.VerifySession(tok, now.Add(time.Hour)) {
		t.Error("换口令后旧会话应失效")
	}
}

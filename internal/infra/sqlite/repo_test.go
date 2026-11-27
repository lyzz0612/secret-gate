package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lyzz0612/deploy-center/internal/app"
)

func openRepo(t *testing.T) *Repo {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.Migrate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMigrate_VersionAndIdempotent(t *testing.T) {
	r := openRepo(t)
	var v int
	if err := r.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != SchemaVersion {
		t.Errorf("user_version = %d, 期望 %d", v, SchemaVersion)
	}
	if err := r.Migrate(); err != nil {
		t.Errorf("重复迁移应幂等: %v", err)
	}
}

func TestMeta(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()
	if v, ok, _ := r.GetMeta(ctx, "nope"); ok || v != "" {
		t.Error("不存在的 key 应返回 ok=false")
	}
	if err := r.SetMeta(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := r.GetMeta(ctx, "k"); !ok || v != "v1" {
		t.Errorf("= %q ok=%v", v, ok)
	}
	if err := r.SetMeta(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.GetMeta(ctx, "k"); v != "v2" {
		t.Errorf("覆盖写入失败: %q", v)
	}
}

func TestGroups_CRUD(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()

	g1, err := r.CreateGroup(ctx, "minigames", "小游戏", "2026-11-27T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateGroup(ctx, "minigames/prod", "", "2026-11-27T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	// 重复 path → 冲突
	if _, err := r.CreateGroup(ctx, "minigames", "x", "now"); !errors.Is(err, app.ErrConflict) {
		t.Errorf("重复 path 应报 ErrConflict, got %v", err)
	}

	gs, _ := r.ListGroups(ctx)
	if len(gs) != 2 || gs[0].Path != "minigames" || gs[1].Path != "minigames/prod" {
		t.Errorf("分组列表 = %+v", gs)
	}

	if err := r.RenameGroup(ctx, g1.ID, "新名字"); err != nil {
		t.Fatal(err)
	}
	gs, _ = r.ListGroups(ctx)
	if gs[0].Name != "新名字" {
		t.Errorf("改名失败: %+v", gs[0])
	}
	if err := r.RenameGroup(ctx, 999, "x"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("改不存在的组应 404, got %v", err)
	}
}

func TestSecrets_UpsertAndGet(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()
	g, _ := r.CreateGroup(ctx, "a", "a", "now")

	if err := r.PutSecret(ctx, g.ID, "KEY_A", []byte("ct1"), []byte("nonce1"), []byte("salt1"), "t1"); err != nil {
		t.Fatal(err)
	}
	b, err := r.GetSecret(ctx, g.ID, "KEY_A")
	if err != nil || string(b.Ciphertext) != "ct1" || string(b.Nonce) != "nonce1" || string(b.AADSalt) != "salt1" || b.UpdatedAt != "t1" {
		t.Fatalf("读取 = %+v err=%v", b, err)
	}
	// upsert：全部字段替换
	if err := r.PutSecret(ctx, g.ID, "KEY_A", []byte("ct2"), []byte("nonce2"), []byte("salt2"), "t2"); err != nil {
		t.Fatal(err)
	}
	b, _ = r.GetSecret(ctx, g.ID, "KEY_A")
	if string(b.Ciphertext) != "ct2" || b.UpdatedAt != "t2" {
		t.Errorf("upsert 后 = %+v", b)
	}
	// 不存在
	if _, err := r.GetSecret(ctx, g.ID, "KEY_B"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("应 404, got %v", err)
	}

	// 列表按 key 排序
	r.PutSecret(ctx, g.ID, "KEY_B", []byte("x"), []byte("n"), []byte("s"), "t")
	ms, _ := r.ListSecrets(ctx, g.ID)
	if len(ms) != 2 || ms[0].Key != "KEY_A" || ms[1].Key != "KEY_B" {
		t.Errorf("列表 = %+v", ms)
	}
}

func TestDeleteSecret_ClearsGrants(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()
	g, _ := r.CreateGroup(ctx, "a", "a", "now")
	r.PutSecret(ctx, g.ID, "K", []byte("c"), []byte("n"), []byte("s"), "t")

	var sid int64
	r.db.QueryRow("SELECT id FROM secrets WHERE group_id=? AND key=?", g.ID, "K").Scan(&sid)
	// 指向这条密钥的授权 + 无关授权
	r.db.Exec("INSERT INTO grants(subject_kind, subject_id, target_kind, target_id, created_at) VALUES('user', 1, 'secret', ?, 't')", sid)
	r.db.Exec("INSERT INTO grants(subject_kind, subject_id, target_kind, target_id, created_at) VALUES('user', 1, 'group', ?, 't')", g.ID)

	if err := r.DeleteSecret(ctx, g.ID, "K"); err != nil {
		t.Fatal(err)
	}
	var n int
	r.db.QueryRow("SELECT COUNT(*) FROM grants WHERE target_kind='secret'").Scan(&n)
	if n != 0 {
		t.Errorf("指向密钥的授权应被清理, 剩 %d", n)
	}
	r.db.QueryRow("SELECT COUNT(*) FROM grants WHERE target_kind='group'").Scan(&n)
	if n != 1 {
		t.Errorf("无关授权不应被误删, 剩 %d", n)
	}
	if _, err := r.GetSecret(ctx, g.ID, "K"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("删除后应 404, got %v", err)
	}
	if err := r.DeleteSecret(ctx, g.ID, "K"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("重复删除应 404, got %v", err)
	}
}

func TestDeleteGroup_CascadesDescendants(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()
	p, _ := r.CreateGroup(ctx, "game", "game", "now")
	c, _ := r.CreateGroup(ctx, "game/prod", "", "now")
	other, _ := r.CreateGroup(ctx, "another", "another", "now")

	r.PutSecret(ctx, p.ID, "P_KEY", []byte("c"), []byte("n"), []byte("s"), "t")
	r.PutSecret(ctx, c.ID, "C_KEY", []byte("c"), []byte("n"), []byte("s"), "t")
	r.PutSecret(ctx, other.ID, "O_KEY", []byte("c"), []byte("n"), []byte("s"), "t")

	// 授权：指向父组、子组、子组内密钥、无关组
	var ckeyId, okeyId int64
	r.db.QueryRow("SELECT id FROM secrets WHERE group_id=? AND key=?", c.ID, "C_KEY").Scan(&ckeyId)
	r.db.QueryRow("SELECT id FROM secrets WHERE group_id=? AND key=?", other.ID, "O_KEY").Scan(&okeyId)
	for _, g := range []struct {
		kind string
		id   int64
	}{{"group", p.ID}, {"group", c.ID}, {"secret", ckeyId}, {"group", other.ID}, {"secret", okeyId}} {
		if _, err := r.db.Exec("INSERT INTO grants(subject_kind, subject_id, target_kind, target_id, created_at) VALUES('user', 1, ?, ?, 't')", g.kind, g.id); err != nil {
			t.Fatal(err)
		}
	}

	if err := r.DeleteGroup(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	gs, _ := r.ListGroups(ctx)
	if len(gs) != 1 || gs[0].Path != "another" {
		t.Errorf("删父组应连带删子组, 剩 %+v", gs)
	}
	var n int
	r.db.QueryRow("SELECT COUNT(*) FROM secrets").Scan(&n)
	if n != 1 {
		t.Errorf("只应剩无关组的密钥, 剩 %d", n)
	}
	r.db.QueryRow("SELECT COUNT(*) FROM grants").Scan(&n)
	if n != 2 { // 指向 another 组 + O_KEY
		t.Errorf("只应剩无关授权, 剩 %d", n)
	}
	if err := r.DeleteGroup(ctx, p.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("重复删除应 404, got %v", err)
	}
}

func TestOverview(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()
	c, err := r.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Groups != 0 || c.Secrets != 0 || c.Users != 0 || c.Pending != 0 || c.Providers != 0 {
		t.Errorf("空库计数 = %+v", c)
	}
	r.CreateGroup(ctx, "a", "a", "now")
	r.PutSecret(ctx, 1, "K", []byte("c"), []byte("n"), []byte("s"), "t")
	r.db.Exec("INSERT INTO users(provider_type, external_id, display, kind, status, created_at) VALUES('github_oidc', '1', 'd', 'machine', 'pending', 't')")
	r.db.Exec("INSERT INTO providers(type, name, created_at, updated_at) VALUES('github_oidc', 'p', 't', 't')")
	c, _ = r.Overview(ctx)
	if c.Groups != 1 || c.Secrets != 1 || c.Users != 1 || c.Pending != 1 || c.Providers != 1 {
		t.Errorf("计数 = %+v", c)
	}
}

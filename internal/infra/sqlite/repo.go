// Package sqlite 是 app.Repo 端口的实现：单文件 SQLite，单连接 + WAL。
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/domain"

	_ "modernc.org/sqlite" // 纯 Go 驱动，无 cgo
)

// Repo 实现 app.Repo。
type Repo struct {
	db *sql.DB
}

var _ app.Repo = (*Repo)(nil)

// Open 打开库。PRAGMA 分两层：
//   - DSN _pragma 参数：每个新连接都生效（foreign_keys/busy_timeout 这类 per-conn 项），
//     防御连接重建后静默失效；
//   - 打开后 exec：journal_mode=WAL（持久属性，写进库文件）。
//
// 单连接（MaxOpenConns=1 + 无限生命周期）：单管理员负载下消灭 SQLITE_BUSY。
func Open(path string) (*Repo, error) {
	dsn := "file:" + strings.ReplaceAll(path, "\\", "/") +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=wal_autocheckpoint(256)" +
		"&_pragma=journal_size_limit(4194304)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)
	r := &Repo{db: db}
	if err := r.applyPragmas(); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Repo) applyPragmas() error {
	ctx := context.Background()
	var mode string
	if err := r.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fmt.Errorf("journal_mode: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("journal_mode = %q, 期望 wal", mode)
	}
	return nil
}

func (r *Repo) Close() error { return r.db.Close() }

// isUnique 判断约束冲突（表已存在时由 CreateGroup 转成 ErrConflict）。
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	var ce interface{ Code() int }
	if errors.As(err, &ce) {
		c := ce.Code()
		return c == 19 || c == 2067 // SQLITE_CONSTRAINT / SQLITE_CONSTRAINT_UNIQUE
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ---------- meta ----------

func (r *Repo) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get meta %q: %w", key, err)
	}
	return v, true, nil
}

func (r *Repo) SetMeta(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

// ---------- secret_groups ----------

func (r *Repo) ListGroups(ctx context.Context) ([]domain.SecretGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, path, name, created_at FROM secret_groups ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	// make 而不是 var：空列表要序列化成 [] 而不是 null，前端 map 会炸
	out := make([]domain.SecretGroup, 0)
	for rows.Next() {
		var g domain.SecretGroup
		if err := rows.Scan(&g.ID, &g.Path, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *Repo) CreateGroup(ctx context.Context, path, name, now string) (domain.SecretGroup, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO secret_groups(path, name, created_at) VALUES(?, ?, ?)`, path, name, now)
	if err != nil {
		if isUnique(err) {
			return domain.SecretGroup{}, fmt.Errorf("%w: 分组 %q 已存在", app.ErrConflict, path)
		}
		return domain.SecretGroup{}, fmt.Errorf("create group: %w", err)
	}
	id, _ := res.LastInsertId()
	return domain.SecretGroup{ID: id, Path: path, Name: name, CreatedAt: now}, nil
}

func (r *Repo) RenameGroup(ctx context.Context, id int64, name string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE secret_groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 分组 %d 不存在", app.ErrNotFound, id)
	}
	return nil
}

// DeleteGroup 真删：连带删全部子孙分组、组内密钥（FK CASCADE），
// 并先清掉指向这些组与组内单条密钥的授权（grants 无外键，必须手动）。
func (r *Repo) DeleteGroup(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var path string
	err = tx.QueryRowContext(ctx, `SELECT path FROM secret_groups WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: 分组 %d 不存在", app.ErrNotFound, id)
	}
	if err != nil {
		return err
	}

	// 自身 + 全部子孙（path 字符集无 LIKE 通配符，前缀查询安全）
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM secret_groups WHERE path = ? OR path LIKE ? || '/%'`, path, path)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var i int64
		if err := rows.Scan(&i); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM grants WHERE target_kind = 'secret' AND target_id IN (SELECT id FROM secrets WHERE group_id IN (%s))`, ph), args...); err != nil {
		return fmt.Errorf("清子密钥授权: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM grants WHERE target_kind = 'group' AND target_id IN (%s)`, ph), args...); err != nil {
		return fmt.Errorf("清组授权: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM secret_groups WHERE id IN (%s)`, ph), args...); err != nil {
		return fmt.Errorf("删分组: %w", err)
	}
	return tx.Commit()
}

// ---------- secrets ----------

func (r *Repo) ListSecrets(ctx context.Context, groupID int64) ([]domain.SecretMeta, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, group_id, key, updated_at FROM secrets WHERE group_id = ? ORDER BY key`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	defer rows.Close()
	out := make([]domain.SecretMeta, 0)
	for rows.Next() {
		var m domain.SecretMeta
		if err := rows.Scan(&m.ID, &m.GroupID, &m.Key, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PutSecret upsert：app 层每次写入都换新 salt + 新 nonce。
func (r *Repo) PutSecret(ctx context.Context, groupID int64, key string, ciphertext, nonce, aadSalt []byte, now string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO secrets(group_id, key, ciphertext, nonce, aad_salt, key_version, updated_at)
		VALUES(?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(group_id, key) DO UPDATE SET
			ciphertext = excluded.ciphertext,
			nonce = excluded.nonce,
			aad_salt = excluded.aad_salt,
			key_version = excluded.key_version,
			updated_at = excluded.updated_at`,
		groupID, key, ciphertext, nonce, aadSalt, now)
	if err != nil {
		return fmt.Errorf("put secret: %w", err)
	}
	return nil
}

func (r *Repo) GetSecret(ctx context.Context, groupID int64, key string) (domain.SecretBlob, error) {
	var b domain.SecretBlob
	err := r.db.QueryRowContext(ctx, `SELECT group_id, key, ciphertext, nonce, aad_salt, key_version, updated_at
		FROM secrets WHERE group_id = ? AND key = ?`, groupID, key).
		Scan(&b.GroupID, &b.Key, &b.Ciphertext, &b.Nonce, &b.AADSalt, &b.KeyVersion, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return b, fmt.Errorf("%w: 密钥 %q 不在分组 %d", app.ErrNotFound, key, groupID)
	}
	if err != nil {
		return b, fmt.Errorf("get secret: %w", err)
	}
	return b, nil
}

// DeleteSecret 真删，连带清指向它的授权。
func (r *Repo) DeleteSecret(ctx context.Context, groupID int64, key string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM secrets WHERE group_id = ? AND key = ?`, groupID, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: 密钥 %q 不在分组 %d", app.ErrNotFound, key, groupID)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM grants WHERE target_kind = 'secret' AND target_id = ?`, id); err != nil {
		return fmt.Errorf("清授权: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("删密钥: %w", err)
	}
	return tx.Commit()
}

// ---------- counts ----------

func (r *Repo) Overview(ctx context.Context) (app.OverviewCounts, error) {
	var c app.OverviewCounts
	err := r.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM secret_groups),
		(SELECT COUNT(*) FROM secrets),
		(SELECT COUNT(*) FROM users),
		(SELECT COUNT(*) FROM providers)`).
		Scan(&c.Groups, &c.Secrets, &c.Users, &c.Providers)
	if err != nil {
		return c, fmt.Errorf("overview: %w", err)
	}
	return c, nil
}

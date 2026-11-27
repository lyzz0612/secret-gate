package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed schema.sql
var schemaSQL string

//go:embed migration_v2.sql
var v2SQL string

// SchemaVersion 当前 schema 版本（= migrations 数量）。
const SchemaVersion = 2

var migrations = []string{schemaSQL, v2SQL}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execScript 逐条执行 SQL 脚本。
// schema 里没有含 ";" 的字符串字面量，按 ";" 切分是安全的。
func execScript(ctx context.Context, db execer, script string) error {
	for _, stmt := range strings.Split(script, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(stmt), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// Migrate 按 PRAGMA user_version 逐版迁移，幂等。
func (r *Repo) Migrate() error {
	ctx := context.Background()
	var v int
	if err := r.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("读取 user_version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("数据库 user_version=%d 高于本程序支持的最大版本 %d（程序过旧，请升级）", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := execScript(ctx, tx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移到 v%d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("写 user_version: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

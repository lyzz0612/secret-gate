package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// ---------- providers（实例） ----------

func (r *Repo) ListProviders(ctx context.Context) ([]domain.ProviderInstance, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, type, name, enabled, config_json, rules_json, created_at, updated_at
		FROM providers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	var out []domain.ProviderInstance
	for rows.Next() {
		var p domain.ProviderInstance
		var enabled int
		if err := rows.Scan(&p.ID, &p.Type, &p.Name, &enabled, &p.ConfigJSON, &p.RulesJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) ProviderByID(ctx context.Context, id int64) (*domain.ProviderInstance, error) {
	var p domain.ProviderInstance
	var enabled int
	err := r.db.QueryRowContext(ctx, `SELECT id, type, name, enabled, config_json, rules_json, created_at, updated_at
		FROM providers WHERE id = ?`, id).
		Scan(&p.ID, &p.Type, &p.Name, &enabled, &p.ConfigJSON, &p.RulesJSON, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: Provider 实例 %d 不存在", app.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	p.Enabled = enabled != 0
	return &p, nil
}

func (r *Repo) CreateProvider(ctx context.Context, typ, name, configJSON, rulesJSON, now string) (domain.ProviderInstance, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO providers(type, name, enabled, config_json, rules_json, created_at, updated_at)
		 VALUES(?, ?, 1, ?, ?, ?, ?)`, typ, name, configJSON, rulesJSON, now, now)
	if err != nil {
		return domain.ProviderInstance{}, fmt.Errorf("create provider: %w", err)
	}
	id, _ := res.LastInsertId()
	return domain.ProviderInstance{
		ID: id, Type: typ, Name: name, Enabled: true,
		ConfigJSON: configJSON, RulesJSON: rulesJSON, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (r *Repo) UpdateProvider(ctx context.Context, id int64, name, configJSON, rulesJSON string, enabled bool, now string) error {
	var e int
	if enabled {
		e = 1
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE providers SET name = ?, config_json = ?, rules_json = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		name, configJSON, rulesJSON, e, now, id)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: Provider 实例 %d 不存在", app.ErrNotFound, id)
	}
	return nil
}

// DeleteProvider 真删实例；用户身份键与实例无关，不动用户。
func (r *Repo) DeleteProvider(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: Provider 实例 %d 不存在", app.ErrNotFound, id)
	}
	return nil
}

// ---------- users ----------

func scanUser(row interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	var sourceID sql.NullInt64
	var lastSeen sql.NullString
	if err := row.Scan(&u.ID, &u.ProviderType, &u.ExternalID, &u.Display, &u.Kind, &u.Status, &sourceID, &u.CreatedAt, &lastSeen); err != nil {
		return nil, err
	}
	u.SourceID = sourceID.Int64
	u.LastSeenAt = lastSeen.String
	return &u, nil
}

const userCols = `id, provider_type, external_id, display, kind, status, source_id, created_at, last_seen_at`

func (r *Repo) ListUsers(ctx context.Context, status string) ([]domain.User, error) {
	q := `SELECT ` + userCols + ` FROM users`
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (r *Repo) UserByID(ctx context.Context, id int64) (*domain.User, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 用户 %d 不存在", app.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (r *Repo) UserByKey(ctx context.Context, typ, extID string) (*domain.User, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE provider_type = ? AND external_id = ?`, typ, extID)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 用户 %s:%s", app.ErrNotFound, typ, extID)
	}
	if err != nil {
		return nil, fmt.Errorf("get user by key: %w", err)
	}
	return u, nil
}

func (r *Repo) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	var sourceID any
	if u.SourceID != 0 {
		sourceID = u.SourceID
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO users(provider_type, external_id, display, kind, status, source_id, created_at, last_seen_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ProviderType, u.ExternalID, u.Display, u.Kind, u.Status, sourceID, u.CreatedAt, u.LastSeenAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	u.ID, _ = res.LastInsertId()
	return u, nil
}

func (r *Repo) SetUserStatus(ctx context.Context, id int64, status string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE users SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("set user status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 用户 %d 不存在", app.ErrNotFound, id)
	}
	return nil
}

func (r *Repo) TouchUser(ctx context.Context, id int64, now string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`, now, id)
	return err
}

// DeleteUser 真删：事务里先清它的授权与组成员关系。
func (r *Repo) DeleteUser(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM grants WHERE subject_kind = 'user' AND subject_id = ?`, id); err != nil {
		return fmt.Errorf("清用户授权: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("清组成员: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删用户: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 用户 %d 不存在", app.ErrNotFound, id)
	}
	return tx.Commit()
}

// ---------- user_groups ----------

func (r *Repo) ListUserGroups(ctx context.Context) ([]domain.UserGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, description, created_at FROM user_groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list user groups: %w", err)
	}
	defer rows.Close()
	var out []domain.UserGroup
	for rows.Next() {
		var g domain.UserGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *Repo) UserGroupByID(ctx context.Context, id int64) (*domain.UserGroup, error) {
	var g domain.UserGroup
	err := r.db.QueryRowContext(ctx, `SELECT id, name, description, created_at FROM user_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 用户组 %d 不存在", app.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get user group: %w", err)
	}
	return &g, nil
}

func (r *Repo) CreateUserGroup(ctx context.Context, name, description, now string) (domain.UserGroup, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO user_groups(name, description, created_at) VALUES(?, ?, ?)`, name, description, now)
	if err != nil {
		if isUnique(err) {
			return domain.UserGroup{}, fmt.Errorf("%w: 用户组 %q 已存在", app.ErrConflict, name)
		}
		return domain.UserGroup{}, fmt.Errorf("create user group: %w", err)
	}
	id, _ := res.LastInsertId()
	return domain.UserGroup{ID: id, Name: name, Description: description, CreatedAt: now}, nil
}

// DeleteUserGroup 真删：事务里清掉组授权与成员关系。
func (r *Repo) DeleteUserGroup(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM grants WHERE subject_kind = 'group' AND subject_id = ?`, id); err != nil {
		return fmt.Errorf("清组授权: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("清组成员: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删用户组: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 用户组 %d 不存在", app.ErrNotFound, id)
	}
	return tx.Commit()
}

func (r *Repo) UserGroupIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT group_id FROM user_group_members WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("user group ids: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetGroupMembers 整体替换成员（事务）。
func (r *Repo) SetGroupMembers(ctx context.Context, groupID int64, userIDs []int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE group_id = ?`, groupID); err != nil {
		return fmt.Errorf("清成员: %w", err)
	}
	for _, uid := range userIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_group_members(user_id, group_id) VALUES(?, ?)`, uid, groupID); err != nil {
			if isUnique(err) { // 用户不存在（FK 不满足时 SQLite 报 constraint）
				return fmt.Errorf("%w: 用户 %d 不存在", app.ErrNotFound, uid)
			}
			return fmt.Errorf("加成员: %w", err)
		}
	}
	return tx.Commit()
}

// GroupMembers 组内成员（用户全量信息）。
func (r *Repo) GroupMembers(ctx context.Context, groupID int64) ([]domain.User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT u.id, u.provider_type, u.external_id, u.display, u.kind, u.status, u.source_id, u.created_at, u.last_seen_at
		FROM user_group_members m JOIN users u ON u.id = m.user_id
		WHERE m.group_id = ? ORDER BY u.id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("group members: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// ---------- grants ----------

func (r *Repo) ListGrants(ctx context.Context, subjectKind string, subjectID int64) ([]domain.Grant, error) {
	q := `SELECT id, subject_kind, subject_id, target_kind, target_id, created_at FROM grants`
	args := []any{}
	if subjectKind != "" {
		q += ` WHERE subject_kind = ? AND subject_id = ?`
		args = append(args, subjectKind, subjectID)
	}
	q += ` ORDER BY id`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()
	var out []domain.Grant
	for rows.Next() {
		var g domain.Grant
		if err := rows.Scan(&g.ID, &g.SubjectKind, &g.SubjectID, &g.TargetKind, &g.TargetID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GrantsForSubjects 取主体可达的授权（用户本人 + 所在用户组）。
func (r *Repo) GrantsForSubjects(ctx context.Context, userID int64, groupIDs []int64) ([]domain.Grant, error) {
	q := `SELECT id, subject_kind, subject_id, target_kind, target_id, created_at FROM grants
		WHERE (subject_kind = 'user' AND subject_id = ?)`
	args := []any{userID}
	if len(groupIDs) > 0 {
		ph := ""
		for range groupIDs {
			ph += "?,"
		}
		ph = ph[:len(ph)-1]
		q += ` OR (subject_kind = 'group' AND subject_id IN (` + ph + `))`
		for _, g := range groupIDs {
			args = append(args, g)
		}
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("grants for subjects: %w", err)
	}
	defer rows.Close()
	var out []domain.Grant
	for rows.Next() {
		var g domain.Grant
		if err := rows.Scan(&g.ID, &g.SubjectKind, &g.SubjectID, &g.TargetKind, &g.TargetID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *Repo) CreateGrant(ctx context.Context, g domain.Grant, now string) (domain.Grant, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO grants(subject_kind, subject_id, target_kind, target_id, created_at) VALUES(?, ?, ?, ?, ?)`,
		g.SubjectKind, g.SubjectID, g.TargetKind, g.TargetID, now)
	if err != nil {
		if isUnique(err) {
			return domain.Grant{}, fmt.Errorf("%w: 授权已存在", app.ErrConflict)
		}
		return domain.Grant{}, fmt.Errorf("create grant: %w", err)
	}
	g.ID, _ = res.LastInsertId()
	g.CreatedAt = now
	return g, nil
}

func (r *Repo) DeleteGrant(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM grants WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete grant: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 授权 %d 不存在", app.ErrNotFound, id)
	}
	return nil
}

// SecretByID 单条密钥元数据（单密钥授权展开用）。
func (r *Repo) SecretByID(ctx context.Context, id int64) (*domain.SecretBlob, error) {
	var b domain.SecretBlob
	err := r.db.QueryRowContext(ctx, `SELECT group_id, key, ciphertext, nonce, aad_salt, key_version, updated_at
		FROM secrets WHERE id = ?`, id).
		Scan(&b.GroupID, &b.Key, &b.Ciphertext, &b.Nonce, &b.AADSalt, &b.KeyVersion, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 密钥 %d", app.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get secret by id: %w", err)
	}
	return &b, nil
}

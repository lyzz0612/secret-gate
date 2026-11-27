package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// ---------- providers（实例） ----------

// providerCols 顺序与 scanProvider 一一对应。
const providerCols = `id, type, name, enabled, config_json, rules_json, default_role, auto_group_ids, created_at, updated_at`

func scanProvider(row interface{ Scan(...any) error }) (*domain.ProviderInstance, error) {
	var p domain.ProviderInstance
	var enabled int
	var groupsJSON string
	if err := row.Scan(&p.ID, &p.Type, &p.Name, &enabled, &p.ConfigJSON, &p.RulesJSON,
		&p.DefaultRole, &groupsJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	p.DefaultRole = orDefault(p.DefaultRole, domain.RoleGuest)
	p.AutoGroupIDs = decodeGroupIDs(groupsJSON)
	return &p, nil
}

// decodeGroupIDs 空值也算空数组（nil 切片会序列化成 null，前端 .length 会崩）。
func decodeGroupIDs(s string) []int64 {
	out := make([]int64, 0)
	if s == "" || s == "[]" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = make([]int64, 0)
	}
	return out
}

func encodeGroupIDs(ids []int64) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (r *Repo) ListProviders(ctx context.Context) ([]domain.ProviderInstance, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ProviderInstance, 0)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *Repo) ProviderByID(ctx context.Context, id int64) (*domain.ProviderInstance, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id = ?`, id)
	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: Provider 实例 %d 不存在", app.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	return p, nil
}

// ProviderByName 按接入标识查实例（CI 侧 POST /v1/auth/{name}）。
func (r *Repo) ProviderByName(ctx context.Context, name string) (*domain.ProviderInstance, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE name = ?`, name)
	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 接入标识 %q 不存在", app.ErrNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("get provider by name: %w", err)
	}
	return p, nil
}

func (r *Repo) CreateProvider(ctx context.Context, p domain.ProviderInstance, now string) (domain.ProviderInstance, error) {
	role := orDefault(p.DefaultRole, domain.RoleGuest)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO providers(type, name, enabled, config_json, rules_json, default_role, auto_group_ids, created_at, updated_at)
		 VALUES(?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		p.Type, p.Name, p.ConfigJSON, p.RulesJSON, role, encodeGroupIDs(p.AutoGroupIDs), now, now)
	if err != nil {
		if isUnique(err) {
			return domain.ProviderInstance{}, fmt.Errorf("%w: 接入标识 %q 已被占用", app.ErrConflict, p.Name)
		}
		return domain.ProviderInstance{}, fmt.Errorf("create provider: %w", err)
	}
	id, _ := res.LastInsertId()
	p.ID = id
	p.Enabled = true
	p.DefaultRole = role
	p.AutoGroupIDs = decodeGroupIDs(encodeGroupIDs(p.AutoGroupIDs))
	p.CreatedAt, p.UpdatedAt = now, now
	return p, nil
}

func (r *Repo) UpdateProvider(ctx context.Context, p domain.ProviderInstance, now string) error {
	var e int
	if p.Enabled {
		e = 1
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE providers SET name = ?, config_json = ?, rules_json = ?, default_role = ?, auto_group_ids = ?,
			enabled = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.ConfigJSON, p.RulesJSON, orDefault(p.DefaultRole, domain.RoleGuest),
		encodeGroupIDs(p.AutoGroupIDs), e, now, p.ID)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: 接入标识 %q 已被占用", app.ErrConflict, p.Name)
		}
		return fmt.Errorf("update provider: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: Provider 实例 %d 不存在", app.ErrNotFound, p.ID)
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

// ProvidersUsingGroup 实例数很少（自用），直接全量读再过滤，避免 JSON 文本匹配的脆弱写法。
func (r *Repo) ProvidersUsingGroup(ctx context.Context, groupID int64) ([]string, error) {
	ps, err := r.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, p := range ps {
		for _, g := range p.AutoGroupIDs {
			if g == groupID {
				out = append(out, p.Name)
				break
			}
		}
	}
	return out, nil
}

// ---------- users ----------

func scanUser(row interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	var sourceID sql.NullInt64
	var lastSeen sql.NullString
	var credKind, credLookup sql.NullString
	var credCipher, credNonce, credSalt []byte
	if err := row.Scan(&u.ID, &u.ProviderType, &u.ExternalID, &u.Display, &u.Kind, &u.Role,
		&sourceID, &u.CreatedAt, &lastSeen,
		&credKind, &credLookup, &credCipher, &credNonce, &credSalt); err != nil {
		return nil, err
	}
	u.SourceID = sourceID.Int64
	u.LastSeenAt = lastSeen.String
	u.Role = orDefault(u.Role, domain.RoleGuest)
	u.CredKind = credKind.String
	u.CredLookup = credLookup.String
	u.CredCipher, u.CredNonce, u.CredSalt = credCipher, credNonce, credSalt
	return &u, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

const userCols = `id, provider_type, external_id, display, kind, role, source_id, created_at, last_seen_at,
	cred_kind, cred_lookup, cred_cipher, cred_nonce, cred_salt`

func (r *Repo) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	out := make([]domain.User, 0)
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

// userExec 让插入逻辑在 *sql.DB 与 *sql.Tx 之间复用。
type userExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertUser(ctx context.Context, ex userExec, u domain.User) (domain.User, error) {
	var sourceID any
	if u.SourceID != 0 {
		sourceID = u.SourceID
	}
	var credKind, credLookup any
	if u.CredKind != "" {
		credKind = u.CredKind
	}
	if u.CredLookup != "" {
		credLookup = u.CredLookup
	}
	res, err := ex.ExecContext(ctx,
		`INSERT INTO users(provider_type, external_id, display, kind, role, status, source_id, created_at, last_seen_at,
			cred_kind, cred_lookup, cred_cipher, cred_nonce, cred_salt)
		 VALUES(?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ProviderType, u.ExternalID, u.Display, u.Kind, orDefault(u.Role, domain.RoleGuest), sourceID, u.CreatedAt, u.LastSeenAt,
		credKind, credLookup, u.CredCipher, u.CredNonce, u.CredSalt)
	if err != nil {
		if isUnique(err) {
			return domain.User{}, fmt.Errorf("%w: 用户 %s:%s 已存在", app.ErrConflict, u.ProviderType, u.ExternalID)
		}
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	u.ID, _ = res.LastInsertId()
	return u, nil
}

func (r *Repo) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	return insertUser(ctx, r.db, u)
}

// CreateUserWithGroups 建用户 + 写入初始组成员关系，同一事务：
// 要么"用户 + 组"一起落地，要么都不落地——不留"人建了但组没进去"的半截状态。
func (r *Repo) CreateUserWithGroups(ctx context.Context, u domain.User, groupIDs []int64) (domain.User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()

	created, err := insertUser(ctx, tx, u)
	if err != nil {
		return domain.User{}, err
	}
	seen := map[int64]bool{}
	for _, gid := range groupIDs {
		if seen[gid] {
			continue
		}
		seen[gid] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_group_members(user_id, group_id) VALUES(?, ?)`, created.ID, gid); err != nil {
			return domain.User{}, fmt.Errorf("加成员（组 %d）: %w", gid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.User{}, err
	}
	return created, nil
}

// UserByCredLookup 按 appkey 哈希索引命中内置用户行（命中即认证通过）。
func (r *Repo) UserByCredLookup(ctx context.Context, lookup string) (*domain.User, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE cred_lookup = ?`, lookup)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 无此凭证", app.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get user by cred lookup: %w", err)
	}
	return u, nil
}

func (r *Repo) TouchUser(ctx context.Context, id int64, now string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`, now, id)
	return err
}

// UpdateUserRole 切换用户分级（guest / normal）。
func (r *Repo) UpdateUserRole(ctx context.Context, id int64, role string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id)
	if err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 用户 %d 不存在", app.ErrNotFound, id)
	}
	return nil
}

// UpdateUserCredential 整体替换某用户的内置凭证（轮换：appkey 重新生成 / password 改口令）。
func (r *Repo) UpdateUserCredential(ctx context.Context, id int64, kind, lookup string, cipher, nonce, salt []byte) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET cred_kind = ?, cred_lookup = ?, cred_cipher = ?, cred_nonce = ?, cred_salt = ? WHERE id = ?`,
		kind, lookup, cipher, nonce, salt, id)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: 凭证哈希冲突（几乎不可能，请重试）", app.ErrConflict)
		}
		return fmt.Errorf("update user credential: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: 用户 %d 不存在", app.ErrNotFound, id)
	}
	return nil
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
	out := make([]domain.UserGroup, 0)
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
	out := make([]int64, 0)
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
	cols := strings.ReplaceAll(userCols, "\n\t", " ")
	q := "SELECT u." + strings.ReplaceAll(cols, ", ", ", u.") + `
		FROM user_group_members m JOIN users u ON u.id = m.user_id
		WHERE m.group_id = ? ORDER BY u.id`
	rows, err := r.db.QueryContext(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("group members: %w", err)
	}
	defer rows.Close()
	out := make([]domain.User, 0)
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
	out := make([]domain.Grant, 0)
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
	out := make([]domain.Grant, 0)
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

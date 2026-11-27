package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/app/plugins/appkey"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// UserService 用户管理：内置型（appkey / password）由后台创建、凭证挂用户行；
// 实例型（github_oidc）过准入自动登记。能读什么由授权决定，读真值还是假值由分级决定。
type UserService struct {
	repo     Repo
	registry *plugins.Registry
	creds    credentials
	ts       func() string
}

func NewUserService(repo Repo, registry *plugins.Registry, seal Sealer) *UserService {
	return &UserService{repo: repo, registry: registry, creds: credentials{seal: seal}, ts: func() string { return nowRFC3339() }}
}

func (s *UserService) List(ctx context.Context) ([]domain.User, error) {
	return s.repo.ListUsers(ctx)
}

func (s *UserService) Get(ctx context.Context, id int64) (*domain.User, error) {
	return s.repo.UserByID(ctx, id)
}

// CreateUserReq 后台建内置型用户。Type 只能选内置类型（appkey / password）。
type CreateUserReq struct {
	Type       string // appkey | password
	ExternalID string // appkey: 标识名；password: 用户名（Basic auth 用）
	Display    string
	Password   string // password 类型必填；appkey 忽略（自动生成）
}

// Create 后台建内置型用户，返回新用户与凭证明文（创建即可见；后台随时可 reveal）。
// 默认 guest 级别——要真值需在用户详情里提级 normal。
func (s *UserService) Create(ctx context.Context, req CreateUserReq) (domain.User, string, error) {
	typ := strings.TrimSpace(req.Type)
	req.ExternalID = strings.TrimSpace(req.ExternalID)
	req.Display = strings.TrimSpace(req.Display)
	req.Password = strings.TrimSpace(req.Password)

	pt, ok := s.registry.Get(typ)
	if !ok {
		return domain.User{}, "", fmt.Errorf("%w: 未知插件类型 %q", ErrInvalid, typ)
	}
	if !pt.Builtin() {
		return domain.User{}, "", fmt.Errorf("%w: 类型 %q 是实例型（用户由准入自动登记，不手动创建）", ErrInvalid, typ)
	}
	if req.ExternalID == "" || len(req.ExternalID) > 128 {
		return domain.User{}, "", fmt.Errorf("%w: 标识名必填且不超过 128 字符", ErrInvalid)
	}
	if req.Display == "" {
		req.Display = req.ExternalID
	}
	if len(req.Display) > 128 {
		return domain.User{}, "", fmt.Errorf("%w: 显示名超过 128 字符", ErrInvalid)
	}

	plain := req.Password
	if typ == "password" {
		if plain == "" {
			return domain.User{}, "", fmt.Errorf("%w: 口令必填", ErrInvalid)
		}
		if len(plain) > 256 {
			return domain.User{}, "", fmt.Errorf("%w: 口令超过 256 字符", ErrInvalid)
		}
	} else if typ == "appkey" {
		var err error
		plain, _, err = appkey.Generate()
		if err != nil {
			return domain.User{}, "", fmt.Errorf("生成 appkey: %w", err)
		}
	}

	u, err := s.saveCredential(ctx, domain.User{
		ProviderType: typ,
		ExternalID:   req.ExternalID,
		Display:      req.Display,
		Kind:         "machine",
		Role:         domain.RoleGuest,
		CreatedAt:    s.ts(),
	}, plain)
	return u, plain, err
}

// saveCredential 加密凭证后落库（Create 与 Rotate 共用）。
func (s *UserService) saveCredential(ctx context.Context, u domain.User, plain string) (domain.User, error) {
	cipher, nonce, salt, err := s.creds.encrypt(plain)
	if err != nil {
		return domain.User{}, err
	}
	u.CredKind = u.ProviderType
	u.CredLookup = appkey.HashKey(plain)
	u.CredCipher, u.CredNonce, u.CredSalt = cipher, nonce, salt
	return s.repo.CreateUser(ctx, u)
}

// SetRole 切换分级：guest（只读 key 名，导出值为固定假值）/ normal（真实值）。
func (s *UserService) SetRole(ctx context.Context, id int64, role string) error {
	if role != domain.RoleGuest && role != domain.RoleNormal {
		return fmt.Errorf("%w: role 只能是 guest / normal", ErrInvalid)
	}
	if _, err := s.repo.UserByID(ctx, id); err != nil {
		return err
	}
	return s.repo.UpdateUserRole(ctx, id, role)
}

// RevealCredential 解密用户行上的内置凭证（appkey / 口令明文）。
func (s *UserService) RevealCredential(ctx context.Context, id int64) (string, error) {
	u, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return "", err
	}
	if u.CredKind == "" {
		return "", fmt.Errorf("%w: 实例型用户没有凭证", ErrInvalid)
	}
	return s.creds.open(u)
}

// RotateCredential 轮换凭证：appkey 生成新随机 key；password 用给定新口令
// （留空则随机生成）。返回新明文；旧值立即失效（lookup 被整体替换）。
func (s *UserService) RotateCredential(ctx context.Context, id int64, newPassword string) (string, error) {
	u, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return "", err
	}
	if u.CredKind == "" {
		return "", fmt.Errorf("%w: 实例型用户没有凭证", ErrInvalid)
	}
	newPassword = strings.TrimSpace(newPassword)
	plain := newPassword
	switch u.CredKind {
	case "appkey":
		var err error
		plain, _, err = appkey.Generate()
		if err != nil {
			return "", fmt.Errorf("生成 appkey: %w", err)
		}
	case "password":
		if plain == "" {
			plain, err = appkey.RandomPassword()
			if err != nil {
				return "", fmt.Errorf("生成口令: %w", err)
			}
		}
		if len(plain) > 256 {
			return "", fmt.Errorf("%w: 口令超过 256 字符", ErrInvalid)
		}
	default:
		return "", fmt.Errorf("%w: 未知凭证类型 %q", ErrInvalid, u.CredKind)
	}
	cipher, nonce, salt, err := s.creds.encrypt(plain)
	if err != nil {
		return "", err
	}
	if err := s.repo.UpdateUserCredential(ctx, id, u.CredKind, appkey.HashKey(plain), cipher, nonce, salt); err != nil {
		return "", err
	}
	return plain, nil
}

// Delete 真删，连带清授权与组成员关系。该身份下次过准入会重新登记。
func (s *UserService) Delete(ctx context.Context, id int64) error {
	return s.repo.DeleteUser(ctx, id)
}

// GrantsOf 用户个人授权 + 所在用户组的授权。
func (s *UserService) GrantsOf(ctx context.Context, id int64) ([]domain.Grant, error) {
	if _, err := s.repo.UserByID(ctx, id); err != nil {
		return nil, err
	}
	return s.repo.ListGrants(ctx, "user", id)
}

// GroupsOf 用户所在用户组。
func (s *UserService) GroupsOf(ctx context.Context, id int64) ([]domain.UserGroup, error) {
	ids, err := s.repo.UserGroupIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	all, err := s.repo.ListUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	idset := map[int64]bool{}
	for _, i := range ids {
		idset[i] = true
	}
	var out []domain.UserGroup
	for _, g := range all {
		if idset[g.ID] {
			out = append(out, g)
		}
	}
	return out, nil
}

// ---------- 用户组 ----------

// UserGroupService 用户组管理：批量发授权的主体。
type UserGroupService struct {
	repo Repo
}

func NewUserGroupService(repo Repo) *UserGroupService { return &UserGroupService{repo: repo} }

func (s *UserGroupService) List(ctx context.Context) ([]UserGroupView, error) {
	gs, err := s.repo.ListUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserGroupView, 0, len(gs))
	for _, g := range gs {
		members, err := s.repo.GroupMembers(ctx, g.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, UserGroupView{UserGroup: g, MemberCount: len(members)})
	}
	return out, nil
}

type UserGroupView struct {
	domain.UserGroup
	MemberCount int `json:"member_count"`
}

func (s *UserGroupService) Create(ctx context.Context, name, description string) (domain.UserGroup, error) {
	if name == "" || len(name) > 64 {
		return domain.UserGroup{}, fmt.Errorf("%w: 组名必填且不超过 64 字符", ErrInvalid)
	}
	if len(description) > 256 {
		return domain.UserGroup{}, fmt.Errorf("%w: 描述超过 256 字符", ErrInvalid)
	}
	return s.repo.CreateUserGroup(ctx, name, description, nowRFC3339())
}

// Delete 真删：连带清组授权与成员关系。
func (s *UserGroupService) Delete(ctx context.Context, id int64) error {
	return s.repo.DeleteUserGroup(ctx, id)
}

// Members 组内成员。
func (s *UserGroupService) Members(ctx context.Context, groupID int64) ([]domain.User, error) {
	if _, err := s.repo.UserGroupByID(ctx, groupID); err != nil {
		return nil, err
	}
	return s.repo.GroupMembers(ctx, groupID)
}

// SetMembers 整体替换成员。成员必须是已存在的用户。
func (s *UserGroupService) SetMembers(ctx context.Context, groupID int64, userIDs []int64) error {
	if _, err := s.repo.UserGroupByID(ctx, groupID); err != nil {
		return err
	}
	if len(userIDs) > 1000 {
		return fmt.Errorf("%w: 成员数超过 1000", ErrInvalid)
	}
	return s.repo.SetGroupMembers(ctx, groupID, userIDs)
}

// GrantService 授权管理（主体：用户/用户组；目标：密钥组/单条密钥）。
type GrantService struct {
	repo Repo
}

func NewGrantService(repo Repo) *GrantService { return &GrantService{repo: repo} }

func (s *GrantService) List(ctx context.Context, subjectKind string, subjectID int64) ([]domain.Grant, error) {
	if subjectKind != "" && subjectKind != "user" && subjectKind != "group" {
		return nil, fmt.Errorf("%w: subject_kind 只能是 user / group", ErrInvalid)
	}
	return s.repo.ListGrants(ctx, subjectKind, subjectID)
}

// Create 校验主体与目标都真实存在（不留孤儿授权），再落库。
func (s *GrantService) Create(ctx context.Context, subjectKind string, subjectID int64, targetKind string, targetID int64) (domain.Grant, error) {
	if subjectKind != "user" && subjectKind != "group" {
		return domain.Grant{}, fmt.Errorf("%w: subject_kind 只能是 user / group", ErrInvalid)
	}
	if targetKind != "group" && targetKind != "secret" {
		return domain.Grant{}, fmt.Errorf("%w: target_kind 只能是 group / secret", ErrInvalid)
	}
	// 主体存在性
	if subjectKind == "user" {
		if _, err := s.repo.UserByID(ctx, subjectID); err != nil {
			return domain.Grant{}, err
		}
	} else {
		found := false
		gs, err := s.repo.ListUserGroups(ctx)
		if err != nil {
			return domain.Grant{}, err
		}
		for _, g := range gs {
			if g.ID == subjectID {
				found = true
				break
			}
		}
		if !found {
			return domain.Grant{}, fmt.Errorf("%w: 用户组 %d 不存在", ErrNotFound, subjectID)
		}
	}
	// 目标存在性
	if targetKind == "group" {
		gs, err := s.repo.ListGroups(ctx)
		if err != nil {
			return domain.Grant{}, err
		}
		found := false
		for _, g := range gs {
			if g.ID == targetID {
				found = true
				break
			}
		}
		if !found {
			return domain.Grant{}, fmt.Errorf("%w: 密钥组 %d 不存在", ErrNotFound, targetID)
		}
	} else {
		if _, err := s.repo.SecretByID(ctx, targetID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return domain.Grant{}, fmt.Errorf("%w: 密钥 %d 不存在", ErrNotFound, targetID)
			}
			return domain.Grant{}, err
		}
	}
	return s.repo.CreateGrant(ctx, domain.Grant{
		SubjectKind: subjectKind, SubjectID: subjectID,
		TargetKind: targetKind, TargetID: targetID,
	}, nowRFC3339())
}

func (s *GrantService) Delete(ctx context.Context, id int64) error {
	return s.repo.DeleteGrant(ctx, id)
}

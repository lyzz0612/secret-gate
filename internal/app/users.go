package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// UserService 用户管理：准入通过的身份自动登记；后台也可手动预建（测试与预配置）。
type UserService struct {
	repo     Repo
	registry *plugins.Registry
}

func NewUserService(repo Repo, registry *plugins.Registry) *UserService {
	return &UserService{repo: repo, registry: registry}
}

func (s *UserService) List(ctx context.Context) ([]domain.User, error) {
	return s.repo.ListUsers(ctx)
}

func (s *UserService) Get(ctx context.Context, id int64) (*domain.User, error) {
	return s.repo.UserByID(ctx, id)
}

// Create 后台手动建用户（预配置用）：真实请求来了按 (类型, 外部ID) 命中同一条记录。
func (s *UserService) Create(ctx context.Context, providerType, externalID, display string) (domain.User, error) {
	providerType = strings.TrimSpace(providerType)
	externalID = strings.TrimSpace(externalID)
	if providerType == "" || len(providerType) > 64 {
		return domain.User{}, fmt.Errorf("%w: 类型必填且不超过 64 字符", ErrInvalid)
	}
	if _, ok := s.registry.Get(providerType); !ok {
		return domain.User{}, fmt.Errorf("%w: 未知插件类型 %q", ErrInvalid, providerType)
	}
	if externalID == "" || len(externalID) > 128 {
		return domain.User{}, fmt.Errorf("%w: 外部 ID 必填且不超过 128 字符", ErrInvalid)
	}
	if display == "" {
		display = externalID
	}
	if len(display) > 128 {
		return domain.User{}, fmt.Errorf("%w: 显示名超过 128 字符", ErrInvalid)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.repo.CreateUser(ctx, domain.User{
		ProviderType: providerType,
		ExternalID:   externalID,
		Display:      display,
		Kind:         "machine",
		CreatedAt:    now,
	})
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

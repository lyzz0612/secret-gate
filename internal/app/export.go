package app

import (
	"context"
	"encoding/hex"
	"log"
	"sort"

	"github.com/lyzz0612/deploy-center/internal/domain"
)

// FakeValue guest 用户导出的固定假值：key 名照列，值不可用。
const FakeValue = "fake_value"

// ExportService /v1/export 的用例：按用户当前授权实时展开。
// normal 解密真值；guest 不解密，key 名照列、值一律 FakeValue。
// 授权每次请求实时算，改完立刻生效，不缓存、不需重启。
type ExportService struct {
	repo Repo
	seal Sealer
}

func NewExportService(repo Repo, seal Sealer) *ExportService {
	return &ExportService{repo: repo, seal: seal}
}

// Export 返回用户可见的密钥集合与是否脱敏。
// groupPath 非空时只导出该 path 及其子孙（用户无权则返回空集）。
func (s *ExportService) Export(ctx context.Context, u domain.User, groupPath string) (map[string]string, bool, int, error) {
	groupIDs, err := s.repo.UserGroupIDs(ctx, u.ID)
	if err != nil {
		return nil, false, 0, err
	}
	grants, err := s.repo.GrantsForSubjects(ctx, u.ID, groupIDs)
	if err != nil {
		return nil, false, 0, err
	}
	allGroups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, false, 0, err
	}

	subj := domain.Subject{UserID: u.ID, GroupIDs: groupIDs}
	targets := domain.VisibleTargets(grants, subj)
	paths := domain.ExpandGroupPaths(allGroups, targets.GroupIDs)

	// 可见的组 id 集合（含目标组本身 + 展开的子孙）
	visibleGroupIDs := map[int64]bool{}
	for _, g := range allGroups {
		if _, ok := paths[g.Path]; ok {
			visibleGroupIDs[g.ID] = true
		}
	}

	// ?group= 过滤
	if groupPath != "" {
		filtered := map[int64]bool{}
		found := false
		for _, g := range allGroups {
			if !visibleGroupIDs[g.ID] {
				continue
			}
			if g.Path == groupPath || domain.IsChildPath(g.Path, groupPath) {
				filtered[g.ID] = true
				found = true
			}
		}
		_ = found
		visibleGroupIDs = filtered
	}

	masked := u.Role != domain.RoleNormal
	out := map[string]string{}
	exported := 0

	// 组授权的密钥
	for gid := range visibleGroupIDs {
		metas, err := s.repo.ListSecrets(ctx, gid)
		if err != nil {
			return nil, false, 0, err
		}
		for _, m := range metas {
			if _, dup := out[m.Key]; dup {
				continue
			}
			if masked {
				out[m.Key] = FakeValue
				exported++
				continue
			}
			if v, ok := s.decrypt(ctx, gid, m.Key); ok {
				out[m.Key] = v
				exported++
			}
		}
	}

	// 单条密钥授权（组内可能已导出，去重）
	for sid := range targets.SecretIDs {
		b, err := s.repo.SecretByID(ctx, sid)
		if err != nil {
			continue // 孤儿授权：目标已删
		}
		if _, dup := out[b.Key]; dup {
			continue
		}
		if masked {
			out[b.Key] = FakeValue
			exported++
			continue
		}
		// 该条密钥所在组不在可见集合里也导出（授权到了单条密钥本身）
		if v, ok := s.openBlob(b); ok {
			out[b.Key] = v
			exported++
		}
	}

	// 审计日志行：只记摘要，不记任何密钥内容
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	log.Printf("export user=%d(%s:%s) role=%s masked=%v groups=%d grants=%d out=%d keys=%v",
		u.ID, u.ProviderType, u.ExternalID, u.Role, masked, len(visibleGroupIDs), len(grants), exported, keys)

	return out, masked, exported, nil
}

func (s *ExportService) decrypt(ctx context.Context, groupID int64, key string) (string, bool) {
	b, err := s.repo.GetSecret(ctx, groupID, key)
	if err != nil {
		return "", false
	}
	return s.openBlob(&b)
}

func (s *ExportService) openBlob(b *domain.SecretBlob) (string, bool) {
	aad := []byte("secret:" + hex.EncodeToString(b.AADSalt))
	plain, err := s.seal.Open(b.Ciphertext, b.Nonce, aad)
	if err != nil {
		log.Printf("export: 解密失败 key=%s group=%d err=%v（跳过）", b.Key, b.GroupID, err)
		return "", false
	}
	return string(plain), true
}

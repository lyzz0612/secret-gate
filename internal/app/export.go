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

// SecretEntry 导出的一条密钥：组路径 + key + 值。
// 同名 key 跨组共存（不同分组各自一条）；同一 (group, key) 只出现一次
// （单条授权与组授权指向同一条密钥时去重）。
type SecretEntry struct {
	Group string `json:"group"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

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

// Export 返回用户可见的密钥条目（按 group path + key 字典序）与是否脱敏。
// groupPath 非空时只导出该 path 及其子孙（用户无权则返回空集）。
func (s *ExportService) Export(ctx context.Context, u domain.User, groupPath string) ([]SecretEntry, bool, error) {
	groupIDs, err := s.repo.UserGroupIDs(ctx, u.ID)
	if err != nil {
		return nil, false, err
	}
	grants, err := s.repo.GrantsForSubjects(ctx, u.ID, groupIDs)
	if err != nil {
		return nil, false, err
	}
	allGroups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, false, err
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

	// gid → path（条目里带分组信息）
	pathOf := make(map[int64]string, len(allGroups))
	for _, g := range allGroups {
		pathOf[g.ID] = g.Path
	}

	masked := u.Role != domain.RoleNormal
	// make 而不是 var：空集要序列化成 [] 而不是 null（前端/调用方 .length 会炸）
	entries := make([]SecretEntry, 0)
	seen := map[[2]string]bool{} // (group, key)：单条授权与组授权撞同一条时去重

	// 组授权的密钥
	for gid := range visibleGroupIDs {
		metas, err := s.repo.ListSecrets(ctx, gid)
		if err != nil {
			return nil, false, err
		}
		for _, m := range metas {
			gp := pathOf[gid]
			id := [2]string{gp, m.Key}
			if seen[id] {
				continue
			}
			seen[id] = true
			if masked {
				entries = append(entries, SecretEntry{Group: gp, Key: m.Key, Value: FakeValue})
				continue
			}
			if v, ok := s.decrypt(ctx, gid, m.Key); ok {
				entries = append(entries, SecretEntry{Group: gp, Key: m.Key, Value: v})
			}
		}
	}

	// 单条密钥授权（可能与组内条目重复，按 (group, key) 去重）
	for sid := range targets.SecretIDs {
		b, err := s.repo.SecretByID(ctx, sid)
		if err != nil {
			continue // 孤儿授权：目标已删
		}
		gp := pathOf[b.GroupID]
		if gp == "" {
			continue // 所在组已不存在（防御，正常被 FK CASCADE 清掉）
		}
		id := [2]string{gp, b.Key}
		if seen[id] {
			continue
		}
		seen[id] = true
		if masked {
			entries = append(entries, SecretEntry{Group: gp, Key: b.Key, Value: FakeValue})
			continue
		}
		// 该条密钥所在组不在可见集合里也导出（授权到了单条密钥本身）
		if v, ok := s.openBlob(b); ok {
			entries = append(entries, SecretEntry{Group: gp, Key: b.Key, Value: v})
		}
	}

	// 确定性输出：group path + key 字典序
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Group != entries[j].Group {
			return entries[i].Group < entries[j].Group
		}
		return entries[i].Key < entries[j].Key
	})

	// 审计日志行：只记摘要，不记任何密钥内容
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Group+"/"+e.Key)
	}
	log.Printf("export user=%d(%s:%s) role=%s masked=%v groups=%d grants=%d out=%d keys=%v",
		u.ID, u.ProviderType, u.ExternalID, u.Role, masked, len(visibleGroupIDs), len(grants), len(entries), names)

	return entries, masked, nil
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

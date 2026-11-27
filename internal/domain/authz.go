package domain

import "strings"

// Subject 一次导出请求的主体：用户本人 + 其所在用户组。
type Subject struct {
	UserID   int64
	GroupIDs []int64
}

// Targets 可见目标集合：密钥组 id 与单条密钥 id 的并集。
type Targets struct {
	GroupIDs  map[int64]struct{}
	SecretIDs map[int64]struct{}
}

// VisibleTargets 计算主体可达的密钥目标（个人授权 ∪ 用户组授权，没有 deny 规则）。
// 孤儿目标 id（指向已删除实体）由调用方按全量数据过滤，这里只算集合。
func VisibleTargets(grants []Grant, s Subject) Targets {
	t := Targets{
		GroupIDs:  map[int64]struct{}{},
		SecretIDs: map[int64]struct{}{},
	}
	groups := map[int64]struct{}{}
	for _, g := range s.GroupIDs {
		groups[g] = struct{}{}
	}
	for _, g := range grants {
		var hit bool
		switch g.SubjectKind {
		case "user":
			hit = s.UserID == g.SubjectID
		case "group":
			_, hit = groups[g.SubjectID]
		default:
			continue
		}
		if !hit {
			continue
		}
		switch g.TargetKind {
		case "group":
			t.GroupIDs[g.TargetID] = struct{}{}
		case "secret":
			t.SecretIDs[g.TargetID] = struct{}{}
		}
	}
	return t
}

// ExpandGroupPaths 把选中的密钥组展开成可见 path 集合（含全部子孙）。
// 前缀匹配依赖 naming 收紧的字符集（路径不含 LIKE 通配符）。
func ExpandGroupPaths(groups []SecretGroup, selected map[int64]struct{}) map[string]struct{} {
	direct := map[string]struct{}{}
	for _, g := range groups {
		if _, ok := selected[g.ID]; ok {
			direct[g.Path] = struct{}{}
		}
	}
	out := map[string]struct{}{}
	for _, g := range groups {
		for p := range direct {
			if g.Path == p || IsChildPath(g.Path, p) {
				out[g.Path] = struct{}{}
			}
		}
	}
	return out
}

// IsChildPath 报告 child 是否 parent 的子孙（"minigames/prod" 是 "minigames" 的子）。
// "minigames2" 不是 "minigames" 的子：前缀相同但边界不是 '/'。
func IsChildPath(child, parent string) bool {
	return len(child) > len(parent) &&
		strings.HasPrefix(child, parent) &&
		child[len(parent)] == '/'
}

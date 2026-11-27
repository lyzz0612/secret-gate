package domain

import (
	"reflect"
	"sort"
	"testing"
)

func ids(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] }) // map 遍历序随机，必须排序后断言
	return out
}

func TestVisibleTargets(t *testing.T) {
	// 基础数据：用户 7；用户组 100（用户 7 在内）、用户组 200（不在）
	base := []Grant{
		{ID: 1, SubjectKind: "user", SubjectID: 7, TargetKind: "group", TargetID: 50},
		{ID: 2, SubjectKind: "user", SubjectID: 8, TargetKind: "group", TargetID: 51}, // 别人的授权
		{ID: 3, SubjectKind: "group", SubjectID: 100, TargetKind: "group", TargetID: 52},
		{ID: 4, SubjectKind: "group", SubjectID: 200, TargetKind: "group", TargetID: 53}, // 不在的组
		{ID: 5, SubjectKind: "user", SubjectID: 7, TargetKind: "secret", TargetID: 900},
		{ID: 6, SubjectKind: "unknown", SubjectID: 7, TargetKind: "group", TargetID: 54},  // 非法主体
		{ID: 7, SubjectKind: "user", SubjectID: 7, TargetKind: "unknown", TargetID: 55},   // 非法目标
		{ID: 8, SubjectKind: "user", SubjectID: 7, TargetKind: "group", TargetID: 50},     // 与 1 重复
	}

	t.Run("个人与用户组并集", func(t *testing.T) {
		got := VisibleTargets(base, Subject{UserID: 7, GroupIDs: []int64{100}})
		if !reflect.DeepEqual(ids(got.GroupIDs), []int64{50, 52}) {
			t.Errorf("组目标 = %v, 期望 [50 52]", ids(got.GroupIDs))
		}
		if !reflect.DeepEqual(ids(got.SecretIDs), []int64{900}) {
			t.Errorf("密钥目标 = %v, 期望 [900]", ids(got.SecretIDs))
		}
	})

	t.Run("无任何授权", func(t *testing.T) {
		got := VisibleTargets(nil, Subject{UserID: 7})
		if len(got.GroupIDs) != 0 || len(got.SecretIDs) != 0 {
			t.Errorf("应为空: %v %v", got.GroupIDs, got.SecretIDs)
		}
	})

	t.Run("不在用户组则组授权失效", func(t *testing.T) {
		got := VisibleTargets(base, Subject{UserID: 7})
		if !reflect.DeepEqual(ids(got.GroupIDs), []int64{50}) {
			t.Errorf("组目标 = %v, 期望 [50]", ids(got.GroupIDs))
		}
	})

	t.Run("空主体", func(t *testing.T) {
		got := VisibleTargets(base, Subject{})
		if len(got.GroupIDs) != 0 {
			t.Errorf("应为空: %v", got.GroupIDs)
		}
	})

	t.Run("多用户组成员", func(t *testing.T) {
		gs := []Grant{{SubjectKind: "group", SubjectID: 100, TargetKind: "group", TargetID: 52},
			{SubjectKind: "group", SubjectID: 200, TargetKind: "group", TargetID: 53}}
		got := VisibleTargets(gs, Subject{UserID: 7, GroupIDs: []int64{100, 200}})
		if len(got.GroupIDs) != 2 {
			t.Errorf("组目标 = %v, 期望 2 个", got.GroupIDs)
		}
	})
}

func TestExpandGroupPaths(t *testing.T) {
	groups := []SecretGroup{
		{ID: 1, Path: "minigames"},
		{ID: 2, Path: "minigames/prod"},
		{ID: 3, Path: "minigames/dev"},
		{ID: 4, Path: "other"},
		{ID: 5, Path: "other/prod"},
	}

	t.Run("选中父组含子孙", func(t *testing.T) {
		got := ExpandGroupPaths(groups, map[int64]struct{}{1: {}})
		want := map[string]struct{}{"minigames": {}, "minigames/prod": {}, "minigames/dev": {}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("= %v, 期望 %v", got, want)
		}
	})

	t.Run("选中子组不含兄弟", func(t *testing.T) {
		got := ExpandGroupPaths(groups, map[int64]struct{}{2: {}})
		want := map[string]struct{}{"minigames/prod": {}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("= %v, 期望 %v", got, want)
		}
	})

	t.Run("跨项目不串", func(t *testing.T) {
		got := ExpandGroupPaths(groups, map[int64]struct{}{4: {}})
		if _, ok := got["minigames/prod"]; ok {
			t.Error("other 不应展开出 minigames 的组")
		}
		if len(got) != 2 {
			t.Errorf("= %v, 期望 2 个", got)
		}
	})

	t.Run("空选择", func(t *testing.T) {
		if got := ExpandGroupPaths(groups, nil); len(got) != 0 {
			t.Errorf("应为空: %v", got)
		}
	})

	t.Run("孤儿 id", func(t *testing.T) {
		if got := ExpandGroupPaths(groups, map[int64]struct{}{999: {}}); len(got) != 0 {
			t.Errorf("应为空: %v", got)
		}
	})

	t.Run("全选", func(t *testing.T) {
		got := ExpandGroupPaths(groups, map[int64]struct{}{1: {}, 4: {}})
		if len(got) != 5 {
			t.Errorf("= %v, 期望 5 个", got)
		}
	})
}

func TestIsChildPath(t *testing.T) {
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"minigames/prod", "minigames", true},
		{"minigames", "minigames", false},
		{"minigames2", "minigames", false},
		{"minigames/prod/x", "minigames", true},
		{"minigames/prod", "minigames/prod", false},
		{"", "minigames", false},
		{"minigames", "minigames/prod", false},
	}
	for _, c := range cases {
		if got := IsChildPath(c.child, c.parent); got != c.want {
			t.Errorf("IsChildPath(%q, %q) = %v, 期望 %v", c.child, c.parent, got, c.want)
		}
	}
}

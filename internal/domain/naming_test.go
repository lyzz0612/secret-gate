package domain

import "testing"

func TestValidGroupPath(t *testing.T) {
	ok := []string{
		"a", "ab", "a1", "minigames", "minigames/prod", "a-b/c-d", "a1/b2", "game0/h5",
	}
	for _, p := range ok {
		if err := ValidGroupPath(p); err != nil {
			t.Errorf("ValidGroupPath(%q) 应通过，报错: %v", p, err)
		}
	}
	bad := []struct {
		path string
		why  string
	}{
		{"", "空"},
		{"/a", "首斜杠产生空段"},
		{"a/", "尾斜杠产生空段"},
		{"a//b", "连续斜杠产生空段"},
		{"A", "大写"},
		{"a/B", "第二段大写"},
		{"a/b/c", "三级"},
		{"-ab", "连字符开头"},
		{"a_b", "下划线"},
		{"a.b", "点号"},
		{"中文", "非 ASCII"},
		{"a/中文", "第二段非 ASCII"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "33 字符段"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/prod", "第二段 33 字符"},
	}
	for _, b := range bad {
		if err := ValidGroupPath(b.path); err == nil {
			t.Errorf("ValidGroupPath(%q) 应报错（%s）", b.path, b.why)
		}
	}
}

func TestValidSecretKey(t *testing.T) {
	ok := []string{"A", "a", "_X", "OSS_KEY", "a1_b2", "A0"}
	for _, k := range ok {
		if err := ValidSecretKey(k); err != nil {
			t.Errorf("ValidSecretKey(%q) 应通过，报错: %v", k, err)
		}
	}
	bad := []struct {
		key string
		why string
	}{
		{"", "空"},
		{"1A", "数字开头"},
		{"A-B", "连字符"},
		{"A B", "空格"},
		{"A.B", "点号"},
		{"中文", "非 ASCII"},
		{"A-B-C", "多连字符"},
	}
	for _, b := range bad {
		if err := ValidSecretKey(b.key); err == nil {
			t.Errorf("ValidSecretKey(%q) 应报错（%s）", b.key, b.why)
		}
	}
	// 长度边界：128 过、129 拒
	long := make([]byte, 128)
	for i := range long {
		long[i] = 'A'
	}
	if err := ValidSecretKey(string(long)); err != nil {
		t.Errorf("128 字符 key 应通过: %v", err)
	}
	if err := ValidSecretKey(string(append(long, 'A'))); err == nil {
		t.Error("129 字符 key 应报错")
	}
}

func TestValidDisplayName(t *testing.T) {
	if err := ValidDisplayName("生产环境"); err != nil {
		t.Errorf("中文名应通过: %v", err)
	}
	for _, n := range []string{"", "   ", "\t\n"} {
		if err := ValidDisplayName(n); err == nil {
			t.Errorf("名字 %q 应报错", n)
		}
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'x'
	}
	if err := ValidDisplayName(string(long)); err == nil {
		t.Error("65 字符名字应报错")
	}
}

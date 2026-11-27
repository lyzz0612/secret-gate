package domain

import (
	"strings"
	"testing"
)

func TestCompile_Errors(t *testing.T) {
	cases := []struct {
		name  string
		rules []Rule
	}{
		{"空规则集", nil},
		{"空列表", []Rule{}},
		{"field 为空", []Rule{{Field: "", Pattern: "x"}}},
		{"field 超长", []Rule{{Field: strings.Repeat("f", 65), Pattern: "x"}}},
		{"pattern 为空", []Rule{{Field: "owner", Pattern: ""}}},
		{"pattern 超长", []Rule{{Field: "owner", Pattern: strings.Repeat("x", 501)}}},
		{"正则不合法", []Rule{{Field: "owner", Pattern: "(unclosed"}}},
		{"正则不合法2", []Rule{{Field: "owner", Pattern: "a[b"}}},
	}
	for _, c := range cases {
		if _, err := Compile(c.rules); err == nil {
			t.Errorf("%s: 应报错", c.name)
		}
	}
	// 规则数上限：32 过、33 拒
	ok32 := make([]Rule, 32)
	for i := range ok32 {
		ok32[i] = Rule{Field: "f", Pattern: "x"}
	}
	if _, err := Compile(ok32); err != nil {
		t.Errorf("32 条规则应通过: %v", err)
	}
	if _, err := Compile(append(ok32, Rule{Field: "f", Pattern: "x"})); err == nil {
		t.Error("33 条规则应报错")
	}
}

func TestMatch(t *testing.T) {
	claims := map[string]string{
		"repository_owner": "lyzz0612",
		"repository_id":    "987654321",
		"event_name":       "push",
		"ref":              "refs/heads/main",
	}
	cases := []struct {
		name    string
		rules   []Rule
		want    bool
		wantHit []bool // 逐条期望
	}{
		{"单条命中", []Rule{{"repository_owner", "lyzz0612"}}, true, []bool{true}},
		{"单条不命中", []Rule{{"repository_owner", "someoneelse"}}, false, []bool{false}},
		{"字段缺失", []Rule{{"environment", "prod"}}, false, []bool{false}},
		{"AND 全命中", []Rule{{"repository_owner", "lyzz0612"}, {"event_name", "push"}}, true, []bool{true, true}},
		{"AND 一条不中", []Rule{{"repository_owner", "lyzz0612"}, {"event_name", "release"}}, false, []bool{true, false}},
		{"锚定: 前缀混淆不匹配", []Rule{{"repository_owner", "lyzz"}}, false, []bool{false}},
		{"锚定: 后缀混淆不匹配", []Rule{{"repository_owner", "0612"}}, false, []bool{false}},
		{"用户自带锚定仍命中", []Rule{{"repository_owner", "^lyzz0612$"}}, true, []bool{true}},
		{"用户自带部分锚定", []Rule{{"ref", "^refs/heads/main$"}}, true, []bool{true}},
		{"交替", []Rule{{"event_name", "push|workflow_dispatch|release"}}, true, []bool{true}},
		{"交替不中", []Rule{{"event_name", "pr|pull_request"}}, false, []bool{false}},
		{"数字字段整串", []Rule{{"repository_id", "987654321"}}, true, []bool{true}},
		{"数字字段前缀不匹配", []Rule{{"repository_id", "987"}}, false, []bool{false}},
		{"大小写敏感", []Rule{{"repository_owner", "LYZZ0612"}}, false, []bool{false}},
		{"点号需转义", []Rule{{"repository_owner", "lyzz061."}}, true, []bool{true}}, // . 通配命中
		{"点号转义后严格", []Rule{{"repository_owner", `lyzz061\.`}}, false, []bool{false}},
		{"中括号字符类", []Rule{{"event_name", "pu(sh|sh)"}}, true, []bool{true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cr, err := Compile(c.rules)
			if err != nil {
				t.Fatalf("编译失败: %v", err)
			}
			got, results := cr.Match(claims)
			if got != c.want {
				t.Errorf("Match = %v, 期望 %v（规则 %v, claims %v）", got, c.want, c.rules, claims)
			}
			for i, r := range results {
				if r.Hit != c.wantHit[i] {
					t.Errorf("规则 %d（%s ~ %s）hit = %v, 期望 %v", i, r.Field, r.Pattern, r.Hit, c.wantHit[i])
				}
			}
		})
	}
}

func TestMatch_MissingFieldFlag(t *testing.T) {
	cr, err := Compile([]Rule{{Field: "environment", Pattern: "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	_, results := cr.Match(map[string]string{"event_name": "push"})
	if len(results) != 1 || !results[0].Missing {
		t.Fatalf("缺失字段应带 Missing 标记: %+v", results)
	}
	if results[0].Hit {
		t.Error("缺失字段不应算命中")
	}
}

func TestMatch_NilCompiled(t *testing.T) {
	var c *CompiledRules
	got, results := c.Match(map[string]string{"a": "b"})
	if got || results != nil {
		t.Errorf("nil 规则集应不匹配且无结果, got %v %v", got, results)
	}
}

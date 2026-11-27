package domain

import (
	"fmt"
	"regexp"
)

// Rule 一条准入规则：claims 里 Field 字段的值必须整串匹配 Pattern。
type Rule struct {
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
}

const (
	// MaxRulePatternLen 限制正则长度，防滥用。
	MaxRulePatternLen = 500
	MaxRuleFieldLen   = 64
	MaxRules          = 32
)

// RuleResult 单条规则的匹配结果（供"试一下"与被拒日志展示）。
type RuleResult struct {
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
	Hit     bool   `json:"hit"`
	Missing bool   `json:"missing"`
}

type compiledRule struct {
	field   string
	pattern string
	re      *regexp.Regexp
}

// CompiledRules 编译后的规则集。
// 匹配语义（四条硬规矩）：规则间 AND；整串锚定；字段缺失即不匹配；保存时编译校验。
type CompiledRules struct {
	rules []compiledRule
}

// Compile 校验并编译规则。规则为空直接报错：无准入规则 = 不放行（fail-closed）。
func Compile(rules []Rule) (*CompiledRules, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("规则为空：没有准入规则等于不放行，请至少配置一条")
	}
	if len(rules) > MaxRules {
		return nil, fmt.Errorf("规则数 %d 超过上限 %d", len(rules), MaxRules)
	}
	out := &CompiledRules{rules: make([]compiledRule, 0, len(rules))}
	for i, r := range rules {
		if r.Field == "" || len(r.Field) > MaxRuleFieldLen {
			return nil, fmt.Errorf("规则 %d：field 为空或超过 %d 字符", i+1, MaxRuleFieldLen)
		}
		if r.Pattern == "" {
			return nil, fmt.Errorf("规则 %d（%s）：pattern 为空", i+1, r.Field)
		}
		if len(r.Pattern) > MaxRulePatternLen {
			return nil, fmt.Errorf("规则 %d（%s）：pattern 超过 %d 字符", i+1, r.Field, MaxRulePatternLen)
		}
		// 整串锚定：自动包 ^(?:...)$。用户自己写了 ^ $ 也无害（RE2 非多行模式下重复锚定恒等）。
		re, err := regexp.Compile("^(?:" + r.Pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("规则 %d（%s）：正则编译失败: %v", i+1, r.Field, err)
		}
		out.rules = append(out.rules, compiledRule{field: r.Field, pattern: r.Pattern, re: re})
	}
	return out, nil
}

// Match 对 claims 求值：全部命中才通过，返回逐条结果。
func (c *CompiledRules) Match(claims map[string]string) (bool, []RuleResult) {
	if c == nil {
		return false, nil
	}
	results := make([]RuleResult, 0, len(c.rules))
	all := true
	for _, r := range c.rules {
		res := RuleResult{Field: r.field, Pattern: r.pattern}
		v, ok := claims[r.field]
		if !ok {
			res.Missing = true // 字段缺失 = 不匹配，绝不当成"无限制"
		} else {
			res.Hit = r.re.MatchString(v)
		}
		if !res.Hit {
			all = false
		}
		results = append(results, res)
	}
	return all, results
}

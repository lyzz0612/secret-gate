package domain

import (
	"fmt"
	"strings"
)

const (
	// MaxPathDepth 密钥组两级：项目 / 环境。
	MaxPathDepth = 2
	maxSegLen    = 32
	maxKeyLen    = 128
	maxNameLen   = 64
)

// ValidGroupPath 校验两级分组路径：每段 [a-z0-9][a-z0-9-]{0,31}。
// 字符集刻意收紧到不含 LIKE 通配符（% _），使前缀查询无需转义。
func ValidGroupPath(path string) error {
	if path == "" {
		return fmt.Errorf("path 不能为空")
	}
	segs := strings.Split(path, "/")
	if len(segs) > MaxPathDepth {
		return fmt.Errorf("path 最多 %d 级（项目/环境），当前 %d 级", MaxPathDepth, len(segs))
	}
	for _, seg := range segs {
		if err := validSlug(seg); err != nil {
			return fmt.Errorf("段 %q: %w", seg, err)
		}
	}
	return nil
}

func validSlug(seg string) error {
	if seg == "" {
		return fmt.Errorf("不能为空（检查连续斜杠或首尾斜杠）")
	}
	if len(seg) > maxSegLen {
		return fmt.Errorf("超过 %d 字符", maxSegLen)
	}
	c := seg[0]
	if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
		return fmt.Errorf("必须以小写字母或数字开头")
	}
	for i := 1; i < len(seg); i++ {
		c := seg[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return fmt.Errorf("只允许小写字母、数字和连字符")
		}
	}
	return nil
}

// ValidSecretKey 校验密钥名：^[A-Za-z_][A-Za-z0-9_]{0,127}$（环境变量风格）。
func ValidSecretKey(key string) error {
	if key == "" {
		return fmt.Errorf("key 不能为空")
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("key 超过 %d 字符", maxKeyLen)
	}
	c := key[0]
	if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_') {
		return fmt.Errorf("key 必须以字母或下划线开头")
	}
	for i := 1; i < len(key); i++ {
		c := key[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return fmt.Errorf("key 只允许字母、数字和下划线")
		}
	}
	return nil
}

// ValidDisplayName 分组显示名非空且长度受限。
func ValidDisplayName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("名称不能为空")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("名称超过 %d 字符", maxNameLen)
	}
	return nil
}

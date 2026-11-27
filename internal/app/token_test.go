package app

import (
	"strings"
	"testing"
	"time"
)

func TestTokenService(t *testing.T) {
	svc, err := NewTokenService([]byte(testMasterKey32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	tok, _ := svc.Issue(42, 2*time.Minute, now)

	t.Run("验签取回 uid", func(t *testing.T) {
		uid, err := svc.Verify(tok, now.Add(time.Minute))
		if err != nil || uid != 42 {
			t.Errorf("= %d %v, 期望 42 nil", uid, err)
		}
	})

	t.Run("过期", func(t *testing.T) {
		if _, err := svc.Verify(tok, now.Add(3*time.Minute)); err == nil {
			t.Errorf("应过期")
		}
	})

	t.Run("篡改 uid", func(t *testing.T) {
		parts := strings.Split(tok, ".")
		parts[1] = "43"
		if _, err := svc.Verify(strings.Join(parts, "."), now.Add(time.Minute)); err == nil {
			t.Errorf("篡改后应无效")
		}
	})

	t.Run("垃圾格式", func(t *testing.T) {
		for _, bad := range []string{"", "v1", "v1.42.123", "v2.42.123.x", "v1.abc.123.x", "v1.0.123.x"} {
			if _, err := svc.Verify(bad, now); err == nil {
				t.Errorf("%q 应无效", bad)
			}
		}
	})

	t.Run("换 MASTER_KEY 后全部失效", func(t *testing.T) {
		other, err := NewTokenService([]byte("another-master-key-32-bytes-abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Verify(tok, now.Add(time.Minute)); err == nil {
			t.Errorf("不同 master key 签的票不应通过")
		}
	})
}

const testMasterKey32 = "test-master-key-32-bytes-abcdefg"

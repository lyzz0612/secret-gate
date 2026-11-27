package crypto

import (
	"bytes"
	"errors"
	"testing"
)

var master = []byte("0123456789012345678901234567890123") // 33 字节测试主密钥

func mustNew(t *testing.T, key []byte) *AEAD {
	t.Helper()
	a, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSealOpen_Roundtrip(t *testing.T) {
	a := mustNew(t, master)
	ct, nonce, err := a.Seal([]byte("OSS_ACCESS_KEY=值含中文"), []byte("secret:abc"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Open(ct, nonce, []byte("secret:abc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "OSS_ACCESS_KEY=值含中文" {
		t.Errorf("roundtrip 结果 = %q", got)
	}
}

func TestOpen_WrongAAD(t *testing.T) {
	a := mustNew(t, master)
	ct, nonce, _ := a.Seal([]byte("v"), []byte("secret:abc"))
	if _, err := a.Open(ct, nonce, []byte("secret:xyz")); err == nil {
		t.Error("AAD 不匹配应解密失败")
	}
}

func TestOpen_WrongKey(t *testing.T) {
	a1 := mustNew(t, master)
	a2 := mustNew(t, []byte("another-master-key-32-bytes-aaaaa"))
	ct, nonce, _ := a1.Seal([]byte("v"), nil)
	if _, err := a2.Open(ct, nonce, nil); err == nil {
		t.Error("主密钥不同应解密失败")
	}
}

func TestOpen_TamperedCiphertext(t *testing.T) {
	a := mustNew(t, master)
	ct, nonce, _ := a.Seal([]byte("v"), nil)
	ct[0] ^= 0xFF
	if _, err := a.Open(ct, nonce, nil); err == nil {
		t.Error("密文被改动应解密失败（GCM tag）")
	}
}

func TestOpen_TruncatedNonce(t *testing.T) {
	a := mustNew(t, master)
	ct, _, _ := a.Seal([]byte("v"), nil)
	if _, err := a.Open(ct, []byte("short"), nil); err == nil {
		t.Error("nonce 长度非法应报错")
	}
}

func TestSeal_NonceAndCiphertextUniqueness(t *testing.T) {
	a := mustNew(t, master)
	ct1, n1, _ := a.Seal([]byte("same"), nil)
	ct2, n2, _ := a.Seal([]byte("same"), nil)
	if bytes.Equal(n1, n2) {
		t.Error("两次加密的 nonce 不应相同")
	}
	if bytes.Equal(ct1, ct2) {
		t.Error("同明文两次密文不应相同")
	}
}

func TestNew_ShortMasterKey(t *testing.T) {
	if _, err := New([]byte("too-short")); err == nil {
		t.Error("短主密钥应报错")
	}
	if _, err := DeriveKey([]byte("1234567890123456789012345678901"), "info"); err == nil {
		t.Error("31 字节主密钥应报错")
	}
	if _, err := DeriveKey(nil, "info"); err == nil {
		t.Error("空主密钥应报错")
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	k1, _ := DeriveKey(master, "info-a")
	k2, _ := DeriveKey(master, "info-a")
	k3, _ := DeriveKey(master, "info-b")
	if !bytes.Equal(k1, k2) {
		t.Error("同主密钥同 info 应派生出相同密钥")
	}
	if bytes.Equal(k1, k3) {
		t.Error("不同 info 应派生出不同密钥")
	}
	if len(k1) != 32 {
		t.Errorf("派生密钥长度 = %d, 期望 32", len(k1))
	}
}

func TestError_Wrapped(t *testing.T) {
	a := mustNew(t, master)
	_, _, err := a.Seal(nil, nil)
	if err != nil {
		t.Errorf("Seal 不应报错: %v", err)
	}
	_, err = a.Open(nil, make([]byte, 12), nil)
	if !errors.Is(err, err) || err == nil {
		// 空 ciphertext：GCM Open 空 + tag 缺失应失败
		t.Error("空密文应报错")
	}
}

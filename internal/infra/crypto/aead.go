// Package crypto 实现密钥封存：HKDF 派生 + AES-256-GCM。
// 明文只存在于调用方内存与本包返回值中，永不落盘。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const masterKeyMinLen = 32

// DeriveKey 从主密钥派生 32 字节派生密钥。主密钥本身不直接参与加密。
func DeriveKey(master []byte, info string) ([]byte, error) {
	if len(master) < masterKeyMinLen {
		return nil, fmt.Errorf("master key 长度 %d，不足 %d 字节", len(master), masterKeyMinLen)
	}
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	return key, nil
}

// AEAD 封存器：同一实例使用同一派生密钥。
type AEAD struct {
	gcm cipher.AEAD
}

// New 用 MASTER_KEY 构造。派生 info 固定，保证同主密钥可解旧数据。
func New(master []byte) (*AEAD, error) {
	key, err := DeriveKey(master, "secret-gate/secrets/v1")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &AEAD{gcm: gcm}, nil
}

// Seal 加密，每次调用生成全新随机 nonce。
func (a *AEAD) Seal(plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, a.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("rand: %w", err)
	}
	ciphertext = a.gcm.Seal(nil, nonce, plaintext, aad)
	return ciphertext, nonce, nil
}

// Open 解密并校验完整性；密钥不匹配、AAD 不对、密文被改动都会失败。
func (a *AEAD) Open(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != a.gcm.NonceSize() {
		return nil, fmt.Errorf("nonce 长度 %d 非法", len(nonce))
	}
	plain, err := a.gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("解密失败（密钥不匹配或数据被改动）: %w", err)
	}
	return plain, nil
}

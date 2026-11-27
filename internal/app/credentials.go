package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/lyzz0612/deploy-center/internal/domain"
)

// credentials 内置用户凭证的加解密：明文（appkey / password）用 MASTER_KEY 派生密钥
// AES-GCM 加密挂在用户行上，后台可随时解密 reveal。
// AAD 用随机盐（与密钥同思路）：改显示名不影响解密，换行解不开。
type credentials struct {
	seal Sealer
}

// credAAD 每条凭证独立随机盐 → AAD，防止把 A 行密文挪到 B 行还能解开。
func credAAD(salt []byte) []byte {
	return []byte("cred:" + hex.EncodeToString(salt))
}

// encrypt 返回 (cipher, nonce, salt)。随机 16 字节盐。
func (c *credentials) encrypt(plain string) (cipher, nonce, salt []byte, err error) {
	salt = make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, nil, fmt.Errorf("生成 salt: %w", err)
	}
	cipher, nonce, err = c.seal.Seal([]byte(plain), credAAD(salt))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("加密凭证: %w", err)
	}
	return cipher, nonce, salt, nil
}

// open 解密用户行上的凭证。
func (c *credentials) open(u *domain.User) (string, error) {
	if len(u.CredCipher) == 0 {
		return "", fmt.Errorf("%w: 用户没有内置凭证", ErrNotFound)
	}
	plain, err := c.seal.Open(u.CredCipher, u.CredNonce, credAAD(u.CredSalt))
	if err != nil {
		return "", fmt.Errorf("解密凭证: %w", err)
	}
	return string(plain), nil
}

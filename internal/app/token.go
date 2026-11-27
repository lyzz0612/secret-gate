package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// TokenService 是 /v1 侧短期访问令牌：凭证过 /v1/auth 后换发，
// /v1/export 只认它。格式 v1.<uid>.<exp>.<sig>，HMAC key 由 MASTER_KEY 派生
// （与 admin session 的派生域分开）。无状态：过期即吊销，用户被删则查无此人。
type TokenService struct {
	key []byte // HKDF(MASTER_KEY, "secret-gate/v1-token")
}

func NewTokenService(masterKey []byte) (*TokenService, error) {
	r := hkdf.New(sha256.New, masterKey, nil, []byte("secret-gate/v1-token"))
	key := make([]byte, 32)
	if _, err := r.Read(key); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	return &TokenService{key: key}, nil
}

// Issue 签发一张短期 token。
func (s *TokenService) Issue(userID int64, ttl time.Duration, now time.Time) (token string, exp time.Time) {
	exp = now.Add(ttl)
	payload := "v1." + strconv.FormatInt(userID, 10) + "." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.sign(payload), exp
}

// Verify 校验并取出用户 ID。
func (s *TokenService) Verify(token string, now time.Time) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return 0, ErrTokenInvalid
	}
	uid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || uid <= 0 {
		return 0, ErrTokenInvalid
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || exp <= now.Unix() {
		return 0, ErrTokenInvalid
	}
	expect := s.sign(parts[0] + "." + parts[1] + "." + parts[2])
	if !hmac.Equal([]byte(expect), []byte(parts[3])) {
		return 0, ErrTokenInvalid
	}
	return uid, nil
}

func (s *TokenService) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

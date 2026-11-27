package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// SessionTTL 后台会话有效期。
const SessionTTL = 7 * 24 * time.Hour

// LoginService 后台单口令登录：口令来自环境变量 ADMIN_PASSWORD，
// 会话是自签 HMAC cookie。改口令 = 改环境变量并重启，所有旧会话自动失效。
type LoginService struct {
	passHash   []byte // sha256(ADMIN_PASSWORD)
	sessionKey []byte // HKDF(ADMIN_PASSWORD, "secret-gate/session")
}

func NewLoginService(password string) *LoginService {
	sum := sha256.Sum256([]byte(password))
	r := hkdf.New(sha256.New, []byte(password), nil, []byte("secret-gate/session"))
	key := make([]byte, 32)
	_, _ = r.Read(key) // hkdf.Reader 不失败；不足长度的场景不存在
	return &LoginService{passHash: sum[:], sessionKey: key}
}

// Check 常量时间口令比较（先各自 sha256 等长化，避免比时泄漏长度）。
func (s *LoginService) Check(password string) bool {
	sum := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(sum[:], s.passHash) == 1
}

// NewSession 签发 "v1.<exp>.<sig>"。
func (s *LoginService) NewSession(now time.Time) (token string, exp time.Time) {
	exp = now.Add(SessionTTL)
	payload := "v1." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.sign(payload), exp
}

// VerifySession 校验会话令牌（版本、有效期、签名）。
func (s *LoginService) VerifySession(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || exp <= now.Unix() {
		return false
	}
	expect := s.sign(parts[0] + "." + parts[1])
	return hmac.Equal([]byte(expect), []byte(parts[2]))
}

func (s *LoginService) sign(payload string) string {
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

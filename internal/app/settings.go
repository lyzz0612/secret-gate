package app

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// SettingsService 后台可改的运行时设置（存 meta 表，改完立即生效）。
// 目前只有一项：/v1 短期 token 的 TTL（分钟）。
type SettingsService struct {
	repo Repo
}

func NewSettingsService(repo Repo) *SettingsService { return &SettingsService{repo: repo} }

const (
	metaTokenTTL  = "token_ttl_minutes"
	DefaultTTLMin = 2
	minTTLMin     = 1
	maxTTLMin     = 1440 // 24 小时上限：两跳换票模式不该有长命票
)

// TokenTTL 当前 token 寿命。缺省 / 非法 / 越界的存量值一律回默认（2 分钟）。
func (s *SettingsService) TokenTTL(ctx context.Context) time.Duration {
	v, ok, err := s.repo.GetMeta(ctx, metaTokenTTL)
	if err != nil || !ok {
		return DefaultTTLMin * time.Minute
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minTTLMin || n > maxTTLMin {
		return DefaultTTLMin * time.Minute
	}
	return time.Duration(n) * time.Minute
}

// SetTokenTTLMinutes 设置 token 寿命（分钟，clamp 到 [1, 1440]）。
func (s *SettingsService) SetTokenTTLMinutes(ctx context.Context, n int) error {
	if n < minTTLMin || n > maxTTLMin {
		return fmt.Errorf("%w: token 寿命需在 %d–%d 分钟之间", ErrInvalid, minTTLMin, maxTTLMin)
	}
	return s.repo.SetMeta(ctx, metaTokenTTL, strconv.Itoa(n))
}

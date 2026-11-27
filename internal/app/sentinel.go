package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	sentinelKey       = "sentinel_v1"
	sentinelPlaintext = "secret-gate/health"
)

// EnsureSentinel 启动自检：库里没有哨兵就写入一条；有则验证当前 MASTER_KEY 能解开。
// 解不开说明主密钥换了（或库换了），直接拒绝启动，
// 避免服务跑起来之后读密钥时才逐条解密失败。
func EnsureSentinel(repo Repo, seal Sealer) error {
	ctx := context.Background()
	v, ok, err := repo.GetMeta(ctx, sentinelKey)
	if err != nil {
		return fmt.Errorf("读取哨兵: %w", err)
	}
	if !ok {
		ct, nonce, err := seal.Seal([]byte(sentinelPlaintext), []byte("sentinel"))
		if err != nil {
			return fmt.Errorf("加密哨兵: %w", err)
		}
		val := hex.EncodeToString(nonce) + ":" + hex.EncodeToString(ct)
		if err := repo.SetMeta(ctx, sentinelKey, val); err != nil {
			return fmt.Errorf("写入哨兵: %w", err)
		}
		return nil
	}
	parts := strings.SplitN(v, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%w: 哨兵记录损坏", ErrKeyMismatch)
	}
	nonce, err1 := hex.DecodeString(parts[0])
	ct, err2 := hex.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("%w: 哨兵记录损坏", ErrKeyMismatch)
	}
	plain, err := seal.Open(ct, nonce, []byte("sentinel"))
	if err != nil || string(plain) != sentinelPlaintext {
		return fmt.Errorf("%w: MASTER_KEY 与现有数据库不匹配（换了变量或换了库）", ErrKeyMismatch)
	}
	return nil
}

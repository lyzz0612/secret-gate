// Package domain 是纯领域层：不 import net/http、database/sql，不读时钟。
// 所有时间从入参传入，核心判定可用表驱动测试完整覆盖。
package domain

// SecretGroup 密钥分组：path 两级（"minigames" / "minigames/prod"）。
type SecretGroup struct {
	ID        int64
	Path      string
	Name      string
	CreatedAt string
}

// SecretMeta 密钥列表行：明文与密文都不出现，只有元信息。
type SecretMeta struct {
	ID        int64
	GroupID   int64
	Key       string
	UpdatedAt string
}

// SecretBlob 一条密钥的密文存储形态。
type SecretBlob struct {
	GroupID    int64
	Key        string
	Ciphertext []byte
	Nonce      []byte
	AADSalt    []byte
	KeyVersion int
	UpdatedAt  string
}

// Grant 一条授权：主体（用户/用户组）→ 目标（密钥组/单条密钥）。
type Grant struct {
	ID          int64
	SubjectKind string // "user" | "group"
	SubjectID   int64
	TargetKind  string // "group" | "secret"
	TargetID    int64
}

// User 被认证的主体（机器或人），身份键 = (ProviderType, ExternalID)。
type User struct {
	ID           int64
	ProviderType string
	ExternalID   string
	Display      string
	Kind         string // "machine" | "human"
	Status       string // "pending" | "active"
	SourceID     int64
	CreatedAt    string
	LastSeenAt   string
}

// ProviderInstance 某插件类型的一个实例：一份配置 + 一套准入规则。
type ProviderInstance struct {
	ID         int64
	Type       string
	Name       string
	Enabled    bool
	ConfigJSON string
	RulesJSON  string
	CreatedAt  string
	UpdatedAt  string
}

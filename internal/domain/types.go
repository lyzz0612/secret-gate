// Package domain 是纯领域层：不 import net/http、database/sql，不读时钟。
// 所有时间从入参传入，核心判定可用表驱动测试完整覆盖。
package domain

// SecretGroup 密钥分组：path 两级（"minigames" / "minigames/prod"）。
type SecretGroup struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// SecretMeta 密钥列表行：明文与密文都不出现，只有元信息。
type SecretMeta struct {
	ID        int64  `json:"id"`
	GroupID   int64  `json:"group_id"`
	Key       string `json:"key"`
	UpdatedAt string `json:"updated_at"`
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
	ID          int64  `json:"id"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   int64  `json:"subject_id"`
	TargetKind  string `json:"target_kind"`
	TargetID    int64  `json:"target_id"`
	CreatedAt   string `json:"created_at"`
}

// User 被认证的主体（机器或人），身份键 = (ProviderType, ExternalID)。
// 过了准入规则即自动登记（没有待批准状态）；能读什么由授权决定。
type User struct {
	ID           int64  `json:"id"`
	ProviderType string `json:"provider_type"`
	ExternalID   string `json:"external_id"`
	Display      string `json:"display"`
	Kind         string `json:"kind"`      // "machine" | "human"
	SourceID     int64  `json:"source_id"` // 首次接纳它的实例
	CreatedAt    string `json:"created_at"`
	LastSeenAt   string `json:"last_seen_at"`
}

// ProviderInstance 某插件类型的一个实例：一份配置 + 一套准入规则。
type ProviderInstance struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	ConfigJSON string `json:"config_json"`
	RulesJSON  string `json:"rules_json"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// UserGroup 用户分组，用来批量发授权。
type UserGroup struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

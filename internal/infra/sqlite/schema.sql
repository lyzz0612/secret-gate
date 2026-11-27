-- Secret Gate schema v1
-- 八张表：meta / users / user_groups / user_group_members / secret_groups / secrets / grants / providers
-- 注意：没有 accounts 表，后台口令来自环境变量 ADMIN_PASSWORD。
-- 注意：secrets 永远只存密文（AES-256-GCM + 每行独立 aad_salt）。

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 被认证的主体（机器/人）。身份键 = 类型 + 外部 ID，不绑 Provider 实例
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  provider_type TEXT NOT NULL,
  external_id   TEXT NOT NULL,
  display       TEXT NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('machine', 'human')),
  status        TEXT NOT NULL CHECK (status IN ('pending', 'active')),
  source_id     INTEGER,
  created_at    TEXT NOT NULL,
  last_seen_at  TEXT,
  UNIQUE (provider_type, external_id)
);

CREATE TABLE user_groups (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);

CREATE TABLE user_group_members (
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, group_id)
);

CREATE TABLE secret_groups (
  id         INTEGER PRIMARY KEY,
  path       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE secrets (
  id          INTEGER PRIMARY KEY,
  group_id    INTEGER NOT NULL REFERENCES secret_groups(id) ON DELETE CASCADE,
  key         TEXT NOT NULL,
  ciphertext  BLOB NOT NULL,
  nonce       BLOB NOT NULL,
  aad_salt    BLOB NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  updated_at  TEXT NOT NULL,
  UNIQUE (group_id, key)
);

CREATE INDEX idx_secrets_group ON secrets(group_id);

-- 授权：多态主体/目标，SQLite 无多态外键 → 删除时应用层事务清理（有测试守着）
CREATE TABLE grants (
  id           INTEGER PRIMARY KEY,
  subject_kind TEXT NOT NULL CHECK (subject_kind IN ('user', 'group')),
  subject_id   INTEGER NOT NULL,
  target_kind  TEXT NOT NULL CHECK (target_kind IN ('group', 'secret')),
  target_id    INTEGER NOT NULL,
  created_at   TEXT NOT NULL,
  UNIQUE (subject_kind, subject_id, target_kind, target_id)
);

CREATE INDEX idx_grants_subject ON grants(subject_kind, subject_id);

-- Provider 实例：类型来自代码注册表，这里只存"哪一个"
CREATE TABLE providers (
  id          INTEGER PRIMARY KEY,
  type        TEXT NOT NULL,
  name        TEXT NOT NULL,
  enabled     INTEGER NOT NULL DEFAULT 1,
  config_json TEXT NOT NULL DEFAULT '{}',
  rules_json  TEXT NOT NULL DEFAULT '[]',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE INDEX idx_providers_type ON providers(type, enabled);

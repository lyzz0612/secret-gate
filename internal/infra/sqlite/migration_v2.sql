-- v2：用户分级 + 内置凭证（appkey / password 用户）
-- role：guest 只能看到 key 名（导出值为固定假值），normal 拿真实值；存量与新建默认 guest。
-- cred_*：内置型用户凭证。cred_lookup = sha256(凭证) hex，appkey 认证按它命中用户行；
--   凭证明文 AES-GCM 加密存 cred_cipher（MASTER_KEY 派生密钥），后台可解密 reveal。
--   部分唯一索引：NULL（实例型用户）不参与，appkey 才有 lookup。

ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'guest';
ALTER TABLE users ADD COLUMN cred_kind TEXT;
ALTER TABLE users ADD COLUMN cred_cipher BLOB;
ALTER TABLE users ADD COLUMN cred_nonce BLOB;
ALTER TABLE users ADD COLUMN cred_salt BLOB;
ALTER TABLE users ADD COLUMN cred_lookup TEXT;

CREATE UNIQUE INDEX idx_users_cred_lookup ON users(cred_lookup) WHERE cred_lookup IS NOT NULL;

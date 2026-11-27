# Secret Gate 规划（暂名，仓库 deploy-center）

> 状态：草案 v1，2026-11-27
> 目标读者：自己（后续实现时对照）

---

## 1. 一句话定位

一个自托管的小服务：让 GitHub Actions 凭一次性的 OIDC 令牌，拿到自己被授权的密钥，**仓库里不再存任何长期凭据**。

## 2. 目标与非目标

### 目标

| # | 目标 | 判据 |
|---|---|---|
| G1 | 单容器单二进制 | 镜像 ≤ 30 MB，常驻内存 ≤ 30 MB |
| G2 | 数据落 SQLite 单文件 | `./data/app.db`，备份 = 拷文件或 `VACUUM INTO` |
| G3 | CI 零长期密钥 | workflow 里只有 `id-token: write`，没有 `secrets.*` |
| G4 | 插件式认证 | 加插件 = 加一个 Go 文件 + 一行注册，前端零改动 |
| G5 | 插件自带后台配置 | 设置页字段、准入规则可选字段，全由插件声明 |
| G6 | 授权按身份/身份组 → 密钥组/单密钥 | 并集语义，无可读性歧义 |
| G7 | 通用侧边栏管理后台 | 侧栏分组 + 二级页 + 抽屉，操作不下沉到主页 |
| G8 | 界面按 frontend-design 技能设计 | 明确的风格方向，不是默认的 AI 后台长相 |
| G9 | 类型与实例分离 | 同类型可建多个实例（不同 audience / 准入范围） |
| G10 | 库里没有明文 | 只拿走 `app.db` 解不开任何密钥；明文只在内存与主动解密的那次响应里 |

### 非目标（明确不做）

- 多租户 SaaS、RBAC 权限矩阵、审批流、合规审计导出
- 密钥版本历史 / 回滚 / 定时轮换 / 动态密钥
- PKI、SSH、k8s operator、CLI、SDK
- 高可用、多实例、水平扩展
- 防主机被完全攻破（主密钥与数据同机，与发布中控台同一取舍）

---

## 3. 领域模型

```
                  ┌──────────────────┐
     请求 ──▶ 匹配 │   PluginType     │ 代码：验真 + 声明配置字段
                  │  github_oidc     │
                  └────────┬─────────┘
                           │ 同类型的所有实例（Provider）逐个试
                           │ 每个 = 一份配置 + 一套准入正则
                           ▼
                  ┌──────────────────┐
                  │    Identity      │ 运行时产物，不落库
                  │ type             │
                  │ external_id      │
                  │ claims{}         │
                  └────────┬─────────┘
                           │ 准入规则（字段正则，AND）
                           ▼
                  ┌──────────────────┐
                  │      User        │ (类型, external_id) 唯一
                  │ kind machine     │ 首次通过 → pending
                  │ status           │ 后台批准 → active
                  └────────┬─────────┘
                           │
            ┌──────────────┴──────────────┐
            │ 个人授权                     │ 所在用户组授权
            ▼                             ▼
      ┌───────────┐                 ┌────────────┐
      │  Grant    │◀────────────────│ UserGroup  │
      └─────┬─────┘                 └────────────┘
            │ 目标
            ├──────────────▶ SecretGroup（含子孙 path）
            └──────────────▶ Secret（单条）
```

### 3.1 核心概念

| 概念 | 是什么 | 谁产生 |
|---|---|---|
| **Identity** | 一次请求认出来的身份（临时） | 插件类型 |
| **User** | 持久化的身份主体 | 认证通过 + 准入通过后自动创建，初始 `pending` |
| **UserGroup** | 用户分组，用来批量发权限 | 后台手动 |
| **SecretGroup** | 密钥分组，`path` 表达层级（`minigames`、`minigames/prod`） | 后台手动 |
| **Secret** | 一条键值 | 后台手动 |
| **Grant** | 一条授权：(主体) → (目标) | 后台手动 |
| **PluginType** | 认证**类型**，代码里的插件（`github_oidc`、`password`） | 编译期注册，后台只读 |
| **Provider** | 某个类型的**实例**：一份配置 + 一套准入规则 | 后台可新增任意多个 |

### 3.2 五个关键划分

**① 后台只有一条口令，没有账号体系。**
`ADMIN_PASSWORD` 从环境变量读，后台就用这一条口令登录，会话 cookie 的签名密钥由它派生。所以库里没有 `accounts` 表——改口令 = 改 compose 里的环境变量 + 重启，所有旧会话自动失效。

`users` 表只存"被认证的主体"（`github:minigames` 这类），跟后台登录完全无关。这也意味着：**账号密码插件产出的身份不用于登录后台**，它只是另一种取密钥的凭证。

**② 准入 ≠ 授权。**

| | 回答什么 | 配在哪 | 不过的后果 |
|---|---|---|---|
| 认证 | token 是真的吗 | 插件内部 | 401 |
| 准入 | 这是我的东西吗 | 插件的 `rules`（字段正则） | 403，**不建用户** |
| 授权 | 能读哪些密钥 | `grants` 表 | 200 + 空列表 |

**③ GitHub 的"用户"是仓库，不是人。**
`external_id = repository_id`（数字，改名字/转移仓库都不变）。分支不进身份，`main` 与 `feature-x` 是同一个 user；分支差异靠 grant 上的条件或独立的准入规则表达。

**④ 类型 ≠ 实例。**

| | 类型（PluginType） | 实例（Provider） |
|---|---|---|
| 是什么 | 一段代码：怎么验、有哪些配置字段 | 一份数据：一个 audience、一套准入正则 |
| 从哪来 | 加 Go 文件 + 一行注册 | 后台"接入"页点新建 |
| 能改吗 | 不能，编译进去的 | 随便加、改、删 |
| 数量 | 一个类型 | 同类型可以有多个 |

所以「GitHub 我的个人号」和「GitHub 另一个组织」是两个实例、同一个类型。请求进来时按类型匹配、按 id 顺序逐个实例试，第一个认证 + 准入都过的胜出。

用户的身份键是 `(类型, external_id)`，不绑实例——同一个仓库被两个实例同时接纳时，只会有**一个**用户，不会重复建。

**⑤ 删除就是删除。** 没有归档、没有软删、没有 `archived_at`。删用户、删密钥、删分组、删实例，全是真删，连带清理干净的关联数据。要"停用"就先删，它下次再来敲门会重新变成 `pending`。

---

## 4. 数据库 schema

单文件 SQLite，八张表。迁移用 `PRAGMA user_version` + 内嵌 SQL。

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

-- 实例元信息：schema 版本、实例 ID、audience
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 注意：没有 accounts 表。后台口令来自环境变量 ADMIN_PASSWORD。

-- 被认证的主体（机器/人）。身份键 = 类型 + 外部 ID，不绑实例
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  provider_type TEXT NOT NULL,          -- 'github_oidc'，实例无关
  external_id   TEXT NOT NULL,
  display       TEXT NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('machine','human')),
  status        TEXT NOT NULL CHECK (status IN ('pending','active')),
  source_id     INTEGER,                -- 首次接纳它的实例，仅作溯源，可为空
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

-- 密钥分组：path 表达层级，深度 ≤ 2（应用层校验）
CREATE TABLE secret_groups (
  id         INTEGER PRIMARY KEY,
  path       TEXT NOT NULL UNIQUE,      -- 'minigames' / 'minigames/prod'
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- 密钥：**明文永不入库**。库里只有密文，密钥在环境变量里
CREATE TABLE secrets (
  id          INTEGER PRIMARY KEY,
  group_id    INTEGER NOT NULL REFERENCES secret_groups(id) ON DELETE CASCADE,
  key         TEXT NOT NULL,
  ciphertext  BLOB NOT NULL,            -- AES-256-GCM 密文 + tag
  nonce       BLOB NOT NULL,            -- 每条独立 12 字节随机
  aad_salt    BLOB NOT NULL,            -- 16 字节随机，作为 AAD，防密文挪位
  key_version INTEGER NOT NULL DEFAULT 1,
  updated_at  TEXT NOT NULL,
  UNIQUE (group_id, key)
);

-- 授权：多态主体/目标，无外键 → 删除时应用层事务清理（需测试覆盖）
CREATE TABLE grants (
  id           INTEGER PRIMARY KEY,
  subject_kind TEXT NOT NULL CHECK (subject_kind IN ('user','group')),
  subject_id   INTEGER NOT NULL,
  target_kind  TEXT NOT NULL CHECK (target_kind IN ('group','secret')),
  target_id    INTEGER NOT NULL,
  created_at   TEXT NOT NULL,
  UNIQUE (subject_kind, subject_id, target_kind, target_id)
);
CREATE INDEX idx_grants_subject ON grants(subject_kind, subject_id);

-- Provider 实例：类型来自代码，这里只存"哪一个"
CREATE TABLE providers (
  id          INTEGER PRIMARY KEY,
  type        TEXT NOT NULL,            -- 'github_oidc'，必须存在于代码注册表
  name        TEXT NOT NULL,            -- '我的个人号' / '某组织'
  enabled     INTEGER NOT NULL DEFAULT 1,
  config_json TEXT NOT NULL DEFAULT '{}',
  rules_json  TEXT NOT NULL DEFAULT '[]',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX idx_providers_type ON providers(type, enabled);
```

插件**类型**不进库：它是编译产物，后台从 `GET /admin/api/plugins` 直接读代码里的注册表。这样不存在"库里有个类型但代码里没有"的不一致。

删除实例：直接删。**不动用户**——用户的身份键跟实例无关，删实例只意味着少了一个入口。用户列表里会显示"来源实例已删除"，仅此而已。

### 为什么 grant 用多态而不是外键

SQLite 不支持多态外键。代价是删用户/删密钥组时要手工清 grant。这是全项目最容易埋 bug 的地方之一，**删除路径必须有测试**（删用户、删用户组、删密钥组、删单条密钥，各覆盖一次）。

### 为什么 secret_group 用 path 而不是 parent_id

`path` 一列就把层级、唯一性、前缀展开全解决了：授权给 `minigames` = 前缀匹配 `minigames/`，不需要递归查询。代价是改名要连带更新子节点，但组名改得极少。

---

## 5. 插件契约（类型 / 实例）

### 5.1 Go 接口（只给形状，不给实现）

```go
// 后台表单字段（由类型声明，实例填值）
type Field struct {
    Key         string   // 存进 config_json 的键
    Label       string
    Help        string
    Type        string   // text | number | bool | enum | list
    Options     []string // Type=enum 时
    Required    bool
    Secret      bool     // 输入框遮罩
    Placeholder string
}

// 准入规则编辑器能选的可匹配字段
type ClaimField struct {
    Key     string // 'repository_owner'
    Label   string // '仓库所属者'
    Example string // 'lyzz0612'
}

// 类型级验真后的中间产物：claims 已解析，但还没核对 audience
type Parsed struct {
    Claims map[string]string // 全部字符串化，供正则匹配
    ExtID  string            // 稳定唯一，e.g. strconv.FormatInt(repoID, 10)
    Disp   string            // 展示名，e.g. 'lyzz0612/minigames'
    Kind   string            // machine | human
}

// PluginType = 代码里的插件类型
type PluginType interface {
    Type() string                  // 'github_oidc'，存进 providers.type
    Label() string                 // 'GitHub Actions (OIDC)'
    Match(req *http.Request) bool  // 这个请求该不该归这个类型管
    ConfigFields() []Field         // 后台设置页
    ClaimFields() []ClaimField     // 准入规则可选字段
    // 类型级：只验真（签名 / iss / exp），每个请求调用一次
    Parse(ctx context.Context, req *http.Request) (*Parsed, error)
    // 实例级：拿 Parsed + 该实例的配置核对（audience 等），每个实例调用一次
    Accept(ctx context.Context, p *Parsed, cfg map[string]any) error
}
```

注册：`internal/app/plugins/registry.go` 里一张 `map[string]PluginType`，加插件 = 一个文件 + 一行。

**为什么拆成 `Parse` + `Accept` 两步**：`Parse` 里要验 RSA 签名（~50µs）和拉 JWKS，一个请求只该做一次；`Accept` 只比 audience 之类的配置项，每个实例各做一次，成本可忽略。这样加三个实例不会让验签成本翻三倍。

### 5.2 为什么不做 Go 动态插件

`.so` 插件要求版本完全一致的 Go 工具链、交叉编译、distroless 下还要保留 CGO 符号——为一个自用服务不值得。编译期注册的"可扩展性"已经足够：新增插件不改动任何已有代码路径。

### 5.3 claims 一律字符串化

`repository_id` 在 JWT 里是 JSON number。统一转成字符串后再做正则匹配：

- 避免 float64 精度/比较问题（这是唯一的授权判据，不能含糊）
- 规则引擎只有一种数据类型，正则表达式对所有字段通用

### 5.4 准入规则

```json
[
  {"field": "repository_owner", "pattern": "^lyzz0612$"},
  {"field": "event_name",       "pattern": "^(push|workflow_dispatch|release)$"}
]
```

四条硬规矩：

1. **整串锚定**：自动包成 `^(?:...)$`，否则 `lyzz0612` 会匹配 `notlyzz0612x`
2. **规则间 AND**：全部命中才过
3. **字段不存在 = 不匹配**：claim 缺失绝不能当成"无限制"
4. **保存时编译校验**：正则写错当场报错，线上请求路径里不做编译失败分支

Go 的 RE2 没有灾难性回溯，正则长度限制 500 字符即可。

### 5.5 试一下

`POST /admin/api/providers/{id}/test`，body 是一段 claims JSON（手贴的，或从最近被拒记录里取），返回逐条规则的命中情况 + 最终结论。约三十行，但能把"正则写错导致静默放行"这类事故挡在配置阶段。

---

## 6. 请求流程

### 6.1 CI 取密钥

```
runner                    服务                          GitHub
  │  GET $ACTIONS_ID_TOKEN_REQUEST_URL&audience=…  │         │
  │────────────────────────────────────────────────────────▶│
  │◀───────────────────── JWT（~5 分钟有效） ────────────────│
  │                                                 │         │
  │  GET /v1/export?group=minigames/prod            │         │
  │  Authorization: Bearer <jwt>                    │         │
  │────────────────────────────────▶                │         │
  │                                 │ 1 找 Match 的类型（github_oidc）
  │                                 │ 2 Parse 一次：JWKS + 验签 + iss/exp
  │                                 │   拉 JWKS（缓存 1h）──────────────▶│
  │                                 │ 3 该类型的 enabled 实例按 id 逐个：
  │                                 │      Accept(aud) → rules(AND)
  │                                 │      第一个全过的胜出
  │                                 │ 4 一个没过 → 403
  │                                 │ 5 resolveUser(类型, external_id)
  │                                 │     无 → 建 pending；有 → 更新 last_seen
  │                                 │ 6 pending → 403
  │                                 │ 7 展开 grants（个人 ∪ 用户组）
  │                                 │ 8 解密 → JSON
  │◀──────────── {"secrets":{...}} ─┤         │
```

多实例的意义就在这里：「我的个人号」和「某组织」两个实例，准入正则不同，一个请求只会命中其中一个；同一个仓库被两个实例都接纳时，按 `(类型, external_id)` 只会落成**一个**用户。

关键性质：**授权是每次请求实时算的**。改完授权立刻生效，不需要重启、不需要等令牌过期。

### 6.2 首次访问 = pending

任何通过准入的陌生身份第一次来，都会建一条 `pending` 的 user：

- 零权限，`/v1/export` 一律 403
- 后台「用户」页顶部出现"待批准 (N)"，侧栏那条同时开始呼吸
- 这样你能看到"有东西在敲门"，而不是被静默拒绝

**为什么建而不是直接拒**：如果只拒绝不记录，`audience` 配错那天你会一头雾水。建 pending 记录 + 界面上显眼提示，是自用工具最实用的排障手段。

### 6.3 被拒记录

准入不过 → 不建用户，返回 403，同时写一行结构化日志（provider、外部 id、失败原因、来源 IP、时间）。后台"接入"页能看到最近 N 条被拒摘要。

---

## 7. 授权判定

纯函数，无 IO：

```
Allow(now, u User, groups []UserGroup, grants []Grant,
      sgroups []SecretGroup, secrets []Secret) Decision
```

规则：

1. 主体集合 = `{u}` ∪ `groups(u)`
2. 目标展开：`target_kind=group` → 该 path 及其所有子孙；`target_kind=secret` → 单条
3. 结果 = 并集，**没有 deny 规则**（要收回就删授权）
4. 只返回存在且已解密的密钥

有 deny 规则会让"谁覆盖谁"变成需要推理的问题，自用工具不值得。

---

## 8. HTTP API

### 8.1 CI 侧

```
GET  /healthz                                    200 ok / 503
GET  /v1/export                                  全部可读密钥
GET  /v1/export?group=minigames/prod             指定组（含子孙）
GET  /v1/export?format=env                       KEY=VALUE 行，便于 >> $GITHUB_ENV
     Authorization: Bearer <插件令牌>
     → 200 {"secrets":{"KEY":"VAL"}}
     → 401 认证失败   → 403 准入不过 / 未批准 / 无授权
     响应头：Cache-Control: no-store
```

### 8.2 后台

```
POST   /admin/api/login                     {password} → cookie（口令来自环境变量）
POST   /admin/api/logout
GET    /admin/api/me
GET    /admin/api/overview                  计数：组/密钥/用户/待批准/实例/最近被拒

# 密钥：组 + 条目，全部真删
GET    /admin/api/groups                    组列表（project/env 两级）
POST   /admin/api/groups                    {path}
PATCH  /admin/api/groups/{id}               {name}
DELETE /admin/api/groups/{id}               连带删子组、密钥、授权
GET    /admin/api/groups/{id}/secrets            列表只回 key + 更新时间 + 掩码
POST   /admin/api/groups/{id}/secrets       {key, value} 新增
PUT    /admin/api/groups/{id}/secrets/{key} {value} 修改
GET    /admin/api/groups/{id}/secrets/{key}/reveal  点眼睛时才调，返回明文
DELETE /admin/api/groups/{id}/secrets/{key} 删除（连带清指向它的 grant）

# 用户
GET    /admin/api/users?status=&q=
GET    /admin/api/users/{id}                详情：授权、所属组、来源实例
PATCH  /admin/api/users/{id}                {status}
DELETE /admin/api/users/{id}                连带清 grants、组关系
POST   /admin/api/users/{id}/approve        批准 + 可选加入组

# 用户组
GET    /admin/api/user-groups
POST   /admin/api/user-groups               {name, description}
PATCH  /admin/api/user-groups/{id}
DELETE /admin/api/user-groups/{id}          连带清成员关系与授权
PUT    /admin/api/user-groups/{id}/members  {user_ids:[]}

# 授权
GET    /admin/api/grants?subject=user:3
POST   /admin/api/grants                    {subject_kind, subject_id, target_kind, target_id}
DELETE /admin/api/grants/{id}
GET    /admin/api/grants/impact?target_kind=group&target_id=5   影响面：会让多少用户可读

# 类型（只读）与实例（可增删改）
GET    /admin/api/plugins                   代码注册的类型清单 + 字段 schema
GET    /admin/api/providers                 实例列表
POST   /admin/api/providers                 {type, name, config, rules}
PATCH  /admin/api/providers/{id}            {name, enabled, config, rules}
DELETE /admin/api/providers/{id}            真删，不动用户
POST   /admin/api/providers/{id}/test       {claims} → 逐条命中

GET    /admin/api/status                    版本、schema 版本、JWKS 缓存、体积
```

**所有 DELETE 都是真删。** 没有 `archived`、没有 `deleted_at`、没有恢复站。删完就没了，所以删除确认弹窗要写清"会连带删掉什么"——但确认之后就是硬删。

---

## 9. 后台界面

### 9.1 信息架构

侧栏分组 + 二级页，所有次要操作（新建、编辑、配置）拆到二级页或右侧抽屉，主界面只留列表和常用操作。

```
┌────────────────┬──────────────────────────────────────────────┐
│  ▓ SECRET GATE │  密钥 / minigames            ⌘K      ⎋      │
│                ├──────────────────────────────────────────────┤
│  总览          │                                              │
│                │  minigames  ›  [ dev ][ staging ][ prod ]    │
│  密钥管理      │  ┌────────────────────────────────────────┐  │
│    密钥    12  │  │ KEY              值              更新   │  │
│                │  │ OSS_ACCESS_KEY   ▪▪▪▪▪▪▪▪  👁  ⧉  11-27│  │
│  访问控制      │  │ SIGN_SECRET      ▪▪▪▪▪▪▪▪  👁  ⧉  11-20│  │
│    授权        │  │ CDN_TOKEN        ▪▪▪▪▪▪▪▪  👁  ⧉  10-02│  │
│    用户     3  │  └────────────────────────────────────────┘  │
│    用户组   1  │  [ + 新增密钥 ]                               │
│                │                                              │
│  接入          │                                              │
│    Provider 2  │                                              │
│                │                                              │
│  系统          │                                              │
│    设置        │                                              │
│                │                                              │
│  ● 运行中  v0.0.7                                             │
└────────────────┴──────────────────────────────────────────────┘
```

| 页面 | 内容 | 必须做好的一件事 |
|---|---|---|
| 总览 | 组数 / 密钥数 / 用户数 / **待批准数** / 实例数 / 最近被拒 | 一屏看出"有没有陌生东西在敲门" |
| 密钥 | 左项目 → 环境 tab → 密钥表；新增/修改/删除 | 列表只有掩码，点眼睛单独取一次明文；随时可看、可改 |
| 授权 | 两种视角：按用户看 / 按密钥看 | 建授权时直接显示"这会让 N 个用户可读" |
| 用户 | 列表（默认过滤 pending）+ 详情抽屉 | 待批准置顶、一键批准并顺手加入组 |
| 用户组 | 组列表 + 成员管理 | 批量勾选加人，一次请求 |
| Provider | 实例卡片列表 → 详情（按类型渲染的配置 + 准入规则 + 试一下） | 新建时先选类型，表单随后长出来 |
| 设置 | 版本、MASTER_KEY 状态、口令来源、备份命令 | MASTER_KEY 异常时红条顶在最上面 |

### 9.2 视觉方向

**定位：工业仪表盘。** 不是 SaaS 后台，是操作台——近黑底、荧光单一强调、等宽字体做主体、1px 分割线、零圆角（或 2px）。看久了不累，截图有辨识度。

**不做的事**：紫色渐变、Inter/Roboto/Arial、卡片堆叠 + 大圆角 + 灰白底、无处不在的阴影。这些是"AI 生成的后台"的默认长相，技能里明确禁止。

| 层 | 字体 | 用途 | 体积 |
|---|---|---|---|
| 标注层 | **Departure Mono** | 侧栏、表头、状态、数字、ID | ~10 KB |
| 阅读层 | **IBM Plex Sans** | 正文、表单标签、说明 | ~60 KB（拉丁子集） |
| 数值层 | **IBM Plex Mono** | 密钥值、路径、claim、日志 | ~50 KB（拉丁子集） |
| 中文回落 | `PingFang SC` / `Microsoft YaHei` / `Noto Sans SC` | 所有中文 | 系统字体，0 下载 |

中日韩字体不内嵌（一套 1–5 MB，跟"体积尽量小"冲突），中文一律走系统栈。三套拉丁字体子集化后合计约 130 KB。

```
色板（CSS 变量）
--bg        #0B0C0E   近黑，略偏冷
--surface   #111419   卡片 / 表格底
--line      #22262D   1px 分割线
--text      #E8EAED   主文本
--muted     #8A929C   次级文本
--accent    #C9F24D   酸绿：当前项、复制成功、强调数字
--warn      #FFB020   琥珀：待批准、准入未配置
--danger    #FF5252   红：删除、被拒、危险操作
```

**唯一的记忆点：敲门。** 有待批准用户时，侧栏「用户」那一条的左侧出现一条 2px 琥珀色竖线，做 2.4s 周期的透明度呼吸。这是整个界面唯一持续动的元素——它把"有陌生身份在尝试接入"变成看得见的东西。其余一切静止。

**动效只有三处**：

1. 列表首次加载，行按 30ms 间隔依次淡入上移 4px（一次性，不重复）
2. 右侧抽屉 220ms 从右滑入，`cubic-bezier(.32,.72,0,1)`
3. 复制按钮点击后 900ms 内变成 `✓` 并转酸绿

其余交互（hover、focus、选中）只用颜色和 1px 线过渡，不做位移。

**排版尺度**：8px 网格；侧栏 220px；表格行高 44px（等宽值好看）；正文 13px / 行高 1.6；页面标题 26px；概览页的数字用 56px Departure Mono 直接顶格排——大数字是这个界面的第二个辨识特征。

### 9.3 技术

Vite + React 19 + Tailwind 4 + Radix 原语（Dialog / DropdownMenu / Tooltip / Tabs）+ Motion。Radix 是代码进仓库，不是运行时大礼包；Motion 只在上面三处动效用到。

构建产物 `web/dist` 用 `//go:embed` 塞进二进制，同源托管，无 CORS。

---

## 10. 代码分层

```
cmd/vaultd/main.go              组装根：开库、读 MASTER_KEY、注册插件、挂路由
internal/
  domain/                       零依赖，不 import sql/http
    types.go                    User/SecretGroup/Secret/Grant/Identity
    rule.go                     准入规则解析与匹配（纯函数）★
    authz.go                    授权并集展开（纯函数）★
    naming.go                   path/key 校验
  app/                          用例层，就地声明所需接口
    ports.go                    Repo / Sealer 接口
    export.go                   ExportSecrets(ctx, ident, groupPath)
    identity.go                 类型匹配 → Parse → 实例择优 → 准入 → resolveUser
    secrets.go  grants.go  users.go  instances.go
    plugins/                    插件类型（代码）
      registry.go               map[string]PluginType
      github_oidc/              Parse：JWKS + 验签 + claims；Accept：核对 aud
      password/                 Parse：表单口令；Accept：空实现
  infra/
    sqlite/                     migrate.go + repo.go（手写 SQL，约 20 条语句）
    crypto/                     AES-256-GCM、HKDF、常量时间比较
    jwks/                       JWKS 拉取与缓存
  httpapi/
    server.go  routes_v1.go  routes_admin.go
    middleware.go               session / provider / origin
    static.go                   //go:embed web/dist
web/                            Vite + React 前端
```

三条硬规矩：

1. `domain` 里不许出现 `sql.`、`http.`、`time.Now()`——时间从入参来
2. 两个入口产出的 `Principal{Kind, Admin bool, Identity *Identity, User *User}` 由中间件统一，用例只认它
3. 实现只在 `main.go` 里 new

---

## 11. 加密与密钥管理

### 11.1 一句话

**拿走到 `app.db` 的人，得到一堆解密不了的字节。** 明文只存在两个地方：进程内存，和授权方/后台主动要求解密的那一次响应。

### 11.2 方案

| 项 | 方案 |
|---|---|
| 主密钥 | 环境变量 `MASTER_KEY`（≥ 32 字节，建议 64 hex）；缺失或过短**拒绝启动** |
| 派生 | `HKDF-SHA256(MASTER_KEY, info="secret-gate/secrets/v1")` → 32 字节，主密钥本身不当加密密钥用 |
| 算法 | AES-256-GCM，每条密钥独立 12 字节随机 nonce |
| AAD | `aad_salt`（每行独立 16 字节随机），AAD = `"secret:" + hex(aad_salt)` |
| 完整性 | GCM 自带 tag，密文被截断/翻转直接解密失败，不是静默出错 |
| 版本 | `key_version` 记这条密文是哪个主密钥版本加的 |
| 后台口令 | 环境变量 `ADMIN_PASSWORD`；先 SHA-256 再常量时间比较，避免比时泄漏长度 |
| 会话 | HMAC cookie，密钥 = `HKDF(ADMIN_PASSWORD, info="session")`，改口令即全体下线；`Path=/admin`、`HttpOnly`、`SameSite=Strict`，非 GET 校验 `Origin` |

### 11.3 为什么 AAD 用随机 salt 而不是 group.path

原方案用 `group.path + key` 做 AAD，有个隐患：**改项目名或改密钥名之后，AAD 变了，旧密文全解不开**。用每行一个随机 salt，改名不影响解密，同时"把 A 组的密文挪到 B 组的行里"依然会失败（salt 对不上）。

顺带避开"插入时还不知道自增 id"的两步写入。

### 11.4 启动自检

`meta` 表里存一条哨兵：用当前 `MASTER_KEY` 加密的固定串 `secret-gate/health`。

启动时解开它，解不开就**拒绝启动**，日志写"MASTER_KEY 与现有数据库不匹配（可能换了变量或换了库）"，不要让它跑起来然后在读密钥时才逐个报错。

### 11.5 明文出现在哪、不出现在哪

| 场景 | 明文 | 说明 |
|---|---|---|
| 写库 | ❌ | 加密后才落盘 |
| 日志 / 错误信息 | ❌ | 任何分支都不允许把值拼进日志 |
| 后台列表接口 | ❌ | 列表只回 `key` + `updated_at` + 掩码 |
| 后台点眼睛 | ✅ | 单独 `GET .../reveal`，点一次取一次 |
| `/v1/export` | ✅ | 只在通过认证 + 准入 + 授权后返回 |
| 内存 | ✅ | 不可避免；Go 的 string 无法可靠擦除，不假装能清 |

**后台不是"只显示一次"**：列表默认不带明文，是为了减少误泄漏面；想看随时点眼睛，随时改、随时赋值，随时都能看到完整值——这跟"一次性展示"是两件事。

### 11.6 主密钥换了怎么办

`key_version` 是给轮换留的口子。两种做法：

- **保守（默认）**：`MASTER_KEY` 一换，旧数据全部解不开。文档里写死一句：**换之前先备份 `MASTER_KEY`，它和 `app.db` 同等重要**。
- **可轮换（可选，M6 后）**：额外接受 `MASTER_KEY_PREVIOUS`，启动时允许解 `key_version=0` 的记录；后台设置页提供"用当前主密钥重加密全部"，跑完把旧变量删掉。

MVP 只做前者，因为自用场景换主密钥本来就不该发生；但表结构已经为后者留好位置。

### 11.7 这个加密不防什么

诚实写清，别把威胁模型吹大：

- 不防**同时拿到 `app.db` 和 compose 环境变量**的人（那就是拿到全部）
- 不防进程内存被 dump
- 不防主机被 root
- 防的是：**单独拿走数据库文件**（备份、误传、`./data` 目录泄漏、云盘同步）、以及**日志/错误里意外打印明文**

顺带一条运维规矩：`MASTER_KEY` 不要和 `app.db` 放在同一个备份里，分开存。

---

## 12. 体积预算

| 项 | 预算 | 说明 |
|---|---|---|
| 镜像 | ≤ 30 MB | `distroless/static` + 静态二进制（带 CA 证书） |
| 二进制 | ≤ 22 MB | `-ldflags "-s -w"`，纯 Go SQLite 驱动 |
| 常驻 RSS | ≤ 30 MB | Go 运行时 + SQLite 页缓存 |
| 前端 bundle | ≤ 200 KB gzip | 内嵌进二进制 |
| 字体子集 | ≤ 140 KB | Departure Mono + IBM Plex Sans/Mono 拉丁子集，中文走系统字体 |
| 数据文件 | 空库 < 100 KB | 一年日常使用 < 5 MB |

编译：`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`。

---

## 13. 部署

单服务一个容器，数据落编排目录下（不用 named volume）。

```yaml
services:
  secret-gate:
    image: ghcr.io/lyzz0612/secret-gate:latest
    container_name: secret-gate
    restart: unless-stopped
    ports: ["127.0.0.1:8788:8788"]
    environment:
      MASTER_KEY: "请替换为至少32字节的随机串"   # 必须改。与 app.db 同等重要，单独备份
      ADMIN_PASSWORD: "请替换为后台登录口令"     # 必须改，改了老会话全部失效
      BASE_URL: "https://secrets.example.com"   # 必须改，用于生成 audience 与后台展示
    volumes:
      - ./data:/app/data
    healthcheck:
      test: ["CMD", "/vaultd", "-healthcheck"]
      interval: 30s
      timeout: 3s
      retries: 3
```

反代由 1Panel 已有链路负责，后端只监听 `127.0.0.1`。

`MASTER_KEY` 与 `ADMIN_PASSWORD` 缺失时**拒绝启动**，错误信息直接说缺哪个——不生成临时值，否则重启后旧密钥全解不开。

`MASTER_KEY` 还要和库对得上：启动时用库里的哨兵验证，对不上就拒绝启动（见 11.4）。**备份 `app.db` 时必须一并记下当时的 `MASTER_KEY`**，两者分开存，缺一个数据就废了。

### 13.1 发版流水线（ghcr，版本号自动 +1）

| 项 | 决定 |
|---|---|
| 触发 | push 到 `main` |
| 版本 | 读所有 `v*` tag → 取最大 semver → **patch +1** → 打 tag 推回 → 用新号打镜像 |
| 镜像标签 | `ghcr.io/lyzz0612/secret-gate:vX.Y.Z`、`:latest`、`:sha-<7位>` |
| 平台 | `linux/amd64`（VPS 架构确定后可加 arm64） |
| 权限 | `contents: write`（推 tag）、`packages: write`（推镜像） |
| 并发 | `concurrency.group=release`、`cancel-in-progress=false`，两个 push 不会抢到同一个号 |
| 循环风险 | 用 `GITHUB_TOKEN` 推的 ref 不会再触发 workflow，安全 |

关键点：**`fetch-depth: 0` + `fetch-tags: true`**，否则浅克隆读不到历史 tag，版本号永远从 v0.0.1 开始。

```yaml
name: release
on:
  push: { branches: [main] }
concurrency: { group: release, cancel-in-progress: false }

jobs:
  release:
    runs-on: ubuntu-latest
    permissions: { contents: write, packages: write }
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0, fetch-tags: true }
      - id: ver
        run: |
          last=$(git tag -l 'v*' | sed 's/^v//' | sort -V | tail -1)
          [ -z "$last" ] && last=0.0.0
          next=$(echo "$last" | awk -F. '{printf "%d.%d.%d", $1, $2, $3+1}')
          echo "version=$next" >> "$GITHUB_OUTPUT"
      - run: |
          git tag "v${{ steps.ver.outputs.version }}"
          git push origin "v${{ steps.ver.outputs.version }}"
      - uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64
          push: true
          build-args: VERSION=${{ steps.ver.outputs.version }}
          tags: |
            ghcr.io/lyzz0612/secret-gate:v${{ steps.ver.outputs.version }}
            ghcr.io/lyzz0612/secret-gate:latest
            ghcr.io/lyzz0612/secret-gate:sha-${{ github.sha }}
```

Dockerfile 多阶段：`node` 阶段编前端 → `golang` 阶段编二进制（`-ldflags "-X main.version=$(VERSION)"`）→ `distroless/static` 出镜像。版本号在后台「设置」页可见。

---

## 14. CI 接入片段（后台自动生成）

```yaml
jobs:
  deploy:
    runs-on: ubuntu-latest
    permissions:
      id-token: write        # 只需要这一条
      contents: read
    steps:
      - uses: actions/checkout@v4
      - name: 载入密钥
        run: |
          TOKEN=$(curl -sS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
            "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=https://secrets.example.com" | jq -r .value)
          curl -sfS -H "Authorization: Bearer $TOKEN" \
            "https://secrets.example.com/v1/export?group=minigames/prod&format=env" \
            | while IFS= read -r line; do
                echo "::add-mask::${line#*=}"
                echo "$line" >> "$GITHUB_ENV"
              done
```

注意 `::add-mask::` 必须在写进 `$GITHUB_ENV` 之前逐条执行。

---

## 15. 里程碑

| # | 内容 | 验收 | 估时 |
|---|---|---|---|
| M1 | 骨架：Go 模块、SQLite 迁移、healthz、Docker 多阶段、compose、`scripts/dev.bat` | `dev.bat` 双击可起，`/healthz` 200，镜像 ≤ 30 MB | 1d |
| M2 | 加密 + 密钥增改删 + 后台登录 + 侧栏骨架与设计系统（色板/字体/组件） | 能建组建密钥并真删；重启后解得开；**换 MASTER_KEY 后启动即被哨兵拦下**；改项目名后仍能解密 | 1.5d |
| M3 | GitHub OIDC 插件 + 准入规则 + `/v1/export` + 试一下 | **真实 workflow 跑通**；签名错/aud 错/过期/越权各返对应码 | 1.5d |
| M4 | 用户管理（列表/详情/批准/删除）+ 用户组 + 授权 | 个人与分组授权并集正确；删用户/删组连带清授权 | 1.5d |
| M5 | Provider 实例管理（增删改 + 试一下）+ 账号密码插件 + 视觉打磨 | 同类型建两个实例各自生效；加类型后后台自动出现配置，前端零改动 | 1.5d |
| M6 | 部署上 1Panel + 真实仓库接管 + README | 至少 2 个仓库改用本服务，移除 `secrets.*`；README 按实拍界面写完 | 1d |

**MVP = M3 结束**，此时已经能替代现有的 CI key 机制。

---

## 16. 测试策略

| 层 | 覆盖 | 目标 |
|---|---|---|
| `domain/rule.go` | 锚定、AND、字段缺失、正则非法、超长、数字字段 | 40+ 表驱动用例 |
| `domain/authz.go` | 个人/组并集、组路径子孙、删授权后的空集、pending 用户 | 30+ 表驱动用例 |
| 实例择优 | 同类型多实例、只命中其一、全都不过、实例 `enabled=0`、类型不匹配、同仓库被两实例接纳只建一个用户 | 10+ |
| OIDC 验签 | 假 JWKS（httptest 生成 RSA 对）：签名错、alg=none、alg=HS256、iss 错、aud 错、exp 过期、nbf 未来、未知 kid、缺 repository_id、新旧 sub 格式 | 12+ |
| 两个入口 | CI 令牌打后台 403、会话打 `/v1` 403、pending 用户 403 | 3 |
| 删除路径 | 删用户 / 用户组 / 密钥组 / 单条密钥 / Provider 实例，授权与成员关系同步清理；删实例不动用户 | 6 |
| 加密 | salt 错位拒绝、nonce 不重复、**改项目/密钥名后仍能解密**、主密钥不匹配时哨兵拦截启动、列表接口不含明文、reveal 需会话 | 6 |
| 端到端 | 起服务 → 造数据 → curl 导出 → 断言内容与响应头 | 1 条主链路 |

先在本地跑通 M3 的真实 workflow，再往下做——符合"先亲手体验再继续"的习惯。

---

## 17. 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 主机被攻破 | 主密钥与数据同机，全丢 | 写进文档，不假装防了；定期备份 |
| **MASTER_KEY 丢失或误换** | 全部密钥不可恢复 | 文档强提示；启动哨兵在换库/换变量时直接拦下；与 db 分开备份 |
| 授权配错静默放行 | 密钥外泄且无告警 | 授权页显示"这条授权波及 N 个身份"；准入用"试一下" |
| 正则写错 | 同上 | 保存时编译校验 + 试一下 |
| 单点故障 | VPS 挂 → 所有仓库构建挂 | 可选：CI 侧缓存上次成功值作为降级（M6 后评估） |
| grant 多态无外键 | 删实体留孤儿授权 → 幽灵权限 | 删除路径全部有测试 |
| pending 堆积 | 用户列表长满陌生身份 | 用户页顶部计数 + 批量勾选删除 |

---

## 18. 已定决策（2026-11-27）

| # | 决策 | 对规划的影响 |
|---|---|---|
| 1 | 后台只要一条 Docker 环境变量口令，不做账号体系 | 删掉 `accounts` 表、scrypt、用户名字段；九张表变八张 |
| 2 | 准入规则由后台配置字段正则 | 规则编辑器按插件声明的 `ClaimFields` 生成，不做硬编码 |
| 3 | 密钥组两级（项目 / 环境）够了 | `secret_groups` 用 `path`，应用层校验深度 ≤ 2 |
| 4 | 不做定时备份 | 不写 cron、不写备份任务；文档里给一条手工 `VACUUM INTO` 命令 |
| 5 | ghcr + push 自动打包，版本号末位自动 +1 | 新增 13.1 节；用 git tag 作为版本序列 |
| 6 | 最后补 README + 本地运行 bat | 新增 19 节，M6 收尾时一起做 |
| 7 | 通用侧边栏管理后台，界面用 frontend-design 技能设计 | 新增 9.2 视觉方向；G7/G8 |
| 8 | 密钥要能新增、修改、删除 | 后台 API 补全 POST/PUT/DELETE |
| 9 | 所有删除都是真删，不归档 | `users.status` 去掉 `disabled`；无 `archived_at`、无恢复站 |
| 10 | Provider 实例与插件类型分开 | `providers` 变实例表（有 id、可多个），类型留在代码；新增 3.2④、5.1 两步接口 |
| 11 | 要有用户管理 | 用户列表/详情/批准/删除 + 用户组，独立页面 |
| 12 | 密钥必须加密入库，密钥来自环境变量；后台与授权方看到的是解密后明文 | 11 节整体重写；`secrets` 加 `aad_salt`；新增哨兵自检与 reveal 接口 |
| 13 | github_oidc 实例**不配置** audience/issuer：issuer 是代码级常量（GHES 走派生插件），`aud` 是普通 claim 由准入规则匹配 | 插件 ConfigFields 清零、ClaimFields 增加 aud、`aud` 建议作为第一条规则（防重放）；配置键严格校验（未声明的键拒绝） |

---

## 19. 本地开发与文档

### 19.1 `scripts/dev.bat`

目标：双击就能跑，不碰 `.env` 文件，环境变量全在 bat 里 `set`。

```
chcp 65001 >nul                       ← 不加这行中文全乱码
检查 go / node 是否在 PATH，缺哪个提示哪个
web/node_modules 不存在 → npm --prefix web install
npm --prefix web run build             ← 产物给 go:embed 用
set MASTER_KEY=dev-only-32-bytes-key-do-not-use-in-prod
set ADMIN_PASSWORD=dev
set BASE_URL=http://127.0.0.1:8788
go run ./cmd/vaultd -addr 127.0.0.1:8788
（另开窗口）start http://127.0.0.1:8788/admin
```

三个注意点：

- **开发用的 `MASTER_KEY` 固定写死**，不随机生成——否则每次重启数据都解不开，你会在第一天就踩一次
- 数据落在 `./data/app.db`，跟容器路径一致，便于拿容器库直接本地复现问题
- 热更新不做：改前端要重跑 `npm run build` 再重启。要更快的循环就单独 `npm run dev` + 代理到 8788

### 19.2 README 结构

| 章节 | 内容 |
|---|---|
| 一句话 | 让 GitHub Actions 凭 OIDC 取密钥，仓库里不存长期凭据 |
| 30 秒上手 | 一段 compose，两个必须改的变量 |
| 接入第一个仓库 | 完整 workflow 片段 + audience 怎么填 |
| 概念 | 身份 / 准入 / 授权 三层，配那张流程图 |
| 插件 | 内置两个，怎么写第三个（一个文件 + 一行注册） |
| 部署 | 1Panel 反代 + 数据目录 + 升级 |
| 备份 | 一条 `VACUUM INTO` 命令，说明为什么不自动 |
| 安全边界 | 威胁模型：不防主机被攻破、pull_request 默认拒绝、审计只记 claims |
| 开发 | `scripts/dev.bat`、`go test ./...`、发版规则 |

README 放最后写，等 M5 界面定型后按实际截图写，避免先写后改。

---

## 20. 仍开放的项

1. **准入粒度**：先只按 `repository_owner` 正则。将来若要"仅限登记过的仓库"，是规则里多一条字段，不改结构。
2. **arm64 镜像**：等确认 VPS 架构后决定要不要双平台构建。
3. **CI 侧降级缓存**：VPS 挂掉会导致所有仓库构建失败，M6 后用真实体验再定要不要在 workflow 里缓存上次的值。
# 实施日志

## 2026-11-27 · GitLab / Vercel 插件 + 接入标识 + 自动登记（schema v3）

### 需求（用户裁定）

1. 新增 gitlab / vercel 两个平台插件，能验它们各自签发的 OIDC 令牌
2. 不新增 key 字段：**实例名就是接入标识**，CI 侧 `POST /v1/auth/{实例名}`
3. 实例可配「验过之后」：默认分级（guest / normal）+ 自动加入的用户组（多组）
4. 自动登记只在首次生效；**不做**"重新套用"按钮；删除用户组前必须校验被实例引用

### 改动（后端）

- **共享验签核心** `internal/infra/oidc`：RS256 + OIDC discovery（`{issuer}/.well-known/openid-configuration` → `jwks_uri`）+ JWKS 按 URL 缓存 1h + 时间窗；`Verify(ctx, token, issuer, jwksURL)` 先比对 `iss` 再联网，令牌里的 iss 只用于比对
- **插件契约**：`Parse(ctx, req, cfg)` 增参——OIDC 插件从实例配置读 `issuer`（GitHub 仍是代码常量）；github_oidc 从 380 行缩到 ~100 行（复用核心）
- **gitlab_oidc**（新）：`issuer` 必填、`jwks_url` 选填；身份键 `<issuer host>#<project_id>`（不同自建实例的 project_id 会撞）；claim 含 ref_path / ref_protected / pipeline_source / environment 等
- **vercel_oidc**（新）：`issuer` 必填（team 形态 `https://oidc.vercel.com/<team>`）；身份键 `project_id`；claim 含 owner / project / environment
- **接入标识**：新路由 `POST /v1/auth/{provider}`，指名只认那条实例；不带名仍走"类型匹配 → 逐实例试"。名字约束 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` + 全局唯一（重名 409）；未知/停用标识 → 401 并点名（`ErrUnknownProvider`）
- **自动登记**：`providers` 加 `default_role`、`auto_group_ids`（migration v3）；`CreateUserWithGroups` 同一事务建用户 + 入组；`ResolveUser` 只在新建时使用实例配置，已存在用户只刷 last_seen
- **删组校验**：`UserGroupService.Delete` 先查 `ProvidersUsingGroup`，被引用 → 409 点名实例列表
- **试一下**：响应带上 `default_role` 与 `auto_groups`，前端在放行后显示"会登记成什么"

### 改动（前端）

- 实例抽屉：接入标识输入 + 「验过之后」块（分级二选一 + 用户组多选，仅首次生效的提示）
- 实例卡片：展示 `POST /v1/auth/{name}` 与复制按钮、默认分级与自动入组摘要
- 「试一下」：样例 claims 按类型给（GitHub / GitLab / Vercel），放行后预览登记结果
- 设置页「接入速查」改成三类 OIDC 的说法

### 验证

- `go vet ./...` 干净；`go test ./... -count=1` 全绿：5 包 64 个测试函数（新增 8 个：GitLab/Vercel 指名换票、未知/停用/签发者不符、自动登记只写一次、删组 409、实例名字符集与重名、自动组必须存在、试一下预览）
- 测试桩 `fakeOIDC` 增加 discovery 端点（三家插件统一走 discovery）；存量用例的实例名从中文改成 ASCII（名字现在是接入标识）
- `npm --prefix web run build` 通过
- 未做：真实 GitLab / Vercel 令牌联调（需要真实实例与项目），留着接入时第一跑验证

### 已知取舍

- 存量实例若用中文命名，升级后编辑保存会报"接入标识只能用小写字母…"——改名一次即可（名字现在是 URL 的一部分）
- Vercel 默认 `aud` 是 `https://vercel.com/<team>`；要绑自己的域名得在 Vercel 侧做 audience 交换（且 `iss` 会变成 team 形态）
- 内部 GitLab 若用企业 CA / 自签证书，容器里仍需 `EXTRA_CA_CERTS` 之类的信任配置（本轮未做）

---

## 2026-11-27 · README 截图 + 本地演示数据

### 改动

- README 加「界面」截图段（9 张，`docs/images/`）与「本地运行」段（`dev.bat` 四步、单文件 `data/app.db`、清库重来）
- 顺手改掉过期的仓库名引用：README 标题、`docs/deploy.md` 与 `selftest.yml` 里的 `lyzz0612/deploy-center`
- 本地库清空后灌了一份演示数据（5 组 / 12 条密钥 / 1 个 github_oidc 实例 / 3 个用户 / 2 个用户组 / 4 条授权），只在本机 `data/app.db` 里，不进仓库

### 验证

- 截图取自本地实例（`-ldflags -X main.version=0.0.1` 的二进制，Chrome 1440×900 @2x），9 张共约 980 KB
- `go vet ./...` / `go test ./... -count=1` 全绿（5 包 57 个测试函数）
- 灌数据时发现一个未修缺陷：用户表 `cred_lookup`（sha256(口令)）的唯一索引对 password 用户同样生效，两个账密账号用同一口令会撞出「用户 xxx 已存在」的误导性报错；绕过办法是口令不同（待定：password 用户是否该写 lookup）

---

## 2026-11-27 · 新建实例黑屏修复

### 根因

`github_oidc` 清空配置字段后 `ConfigFields()` 返回 nil，Go 把 nil 切片序列化成 JSON
`null`；前端 `curType?.config_fields.length` 对 null 取 length → TypeError →
整棵 React 树崩掉（背景近黑 = 黑屏）。与授权页 `null` 是同一类坑，这次炸在组件树上。

### 修复（三层）

1. 后端契约：`ListTypes()` 对 ConfigFields/ClaimFields 做 nil→空数组兜底，列表字段永远是数组
2. 前端防御：`config_fields?.length`、`results ?? []` 可选链
3. **ErrorBoundary**：包住每个页面（key=page 切换重挂载），组件崩了显示错误卡+重试按钮，
   不再整页黑屏

### 验证

- 新增回归断言（plugins 清单必须含 `"config_fields":[]`）；5 包 49 个测试函数全绿
- 真二进制：`config_fields:[]`（github_oidc）/ 数组（password）确认输出形状
- 排障过程踩一次环境坑：端口被上轮残留进程占着，旧二进制在应答——杀进程重验后确认

---

## 2026-11-27 · 后台手动建用户（预配置 / 测试）

### 改动

- `POST /admin/api/users {provider_type, external_id, display?}`：类型必须是已注册插件类型；同键 409
- 用户页「+ 新建用户」弹窗：类型下拉（来自插件清单）、外部 ID（github_oidc 提示填仓库数字 repository_id）、显示名可省
- **预配置语义**：真实令牌敲门按 `(类型, 外部ID)` 命中同一条记录——先建用户配好授权，部署就绪后第一次真实请求即可拿密钥（有测试验证命中不新建）
- repo CreateUser 补 UNIQUE → ErrConflict 映射

### 测试

- 5 包 49 个测试函数全绿；真二进制 E2E：建用户 201 / 重复 409 / 未知类型 400 / 给手动用户授权 201
- 修了一处测试断言写岔（数同键用户数而不是总用户数——数据显示功能本就正确）

---

## 2026-11-27 · 手动自测 workflow + 空列表 null 修复

## 2026-11-27 · 去掉待批准机制（用户裁定）

### 改动

- **过准入规则即自动登记，无待批准**：准入规则就是唯一门槛（owner 限定等），进来了能读多少由授权决定，没授权 = 空集 200（不是 403）
- 删掉整套状态机：`users.status` 恒 'active'（列保留兼容旧库）、批准 API 两条路由、`Approve`/`SetUserStatus`、pending 计数、前端批准按钮/pending 过滤/呼吸点/待批准卡片
- 首次敲门：200 空集 + 自动登记；被拒仍然是 403 且不登记（准入语义不变）

### 测试

- 5 包 48 个测试函数全绿（×2 稳定）
- 真实二进制 E2E：首次敲门 200 空集自动登记 → 授权 → 200 带值 → 错口令 403 → 撤销立即空集；用户接口无 status 字段
- 顺带发现旧测试的一处假阳性：TestPasswordPlugin 的 seed 在没有 github 实例时"碰巧"靠同状态码通过，新语义下暴露；seed 改为按身份键定位用户，不再数总数

---

## 2026-11-27 · github_oidc 实例配置移除（用户裁定）

### 改动

- **issuer**：代码级常量（`NewWithIssuer` 供测试/GHES 派生插件），Parse 先核对 iss 再拉 JWKS——顺带堵住"令牌里的 iss 把服务引向任意 URL"的隐患
- **audience**：从实例必填配置降为普通 claim，进 `ClaimFields` 下拉框，由准入规则匹配（防重放语义不变，只是换了个写法）
- 实例配置字段清零 → 多实例的区分度完全来自不同规则集；配置键严格校验（未声明的键 400）
- 「试一下」回归纯规则测试；删除 tools/mintjwt（issuer 写死后假签发者进不了真二进制，联调靠 go test 的 httptest）
- **顺手修掉一个 M2 就存在的测试闪失败**：`ids()` 遍历 map 顺序随机，断言前未排序，约 50% 概率挂；排序后 domain ×10、全仓 ×3 全绿

### 测试

- 5 包 48 个测试函数全绿；新增「假 issuer → 401 且不拉 JWKS」用例；「aud 规则不匹配 → 403 不建用户」语义保持

---

## 2026-11-27 · M4+M5：用户组授权 + 账号口令插件

### 完成

- **用户组（M4）**：CRUD + 成员整体替换（事务）+ 删组连带清组授权与成员关系；组授权（subject_kind=group）→ 组内成员导出并集
- **账号口令插件（M5）**：`password` 类型，Basic auth；实例配置 username + password（password 声明为 Secret 字段，前端自动遮罩）；常量时间比对（SHA-256 等长化 + subtle）；凭证放 `Parsed.Secret`，绝不进 claims、绝不落日志
- **前端**：用户组页（建组/删除/成员勾选抽屉）、设置页（版本/schema/口令与主密钥说明/备份命令/接入速查）、Provider 表单按 Secret 声明渲染密码框
- 注册表现在两个类型：`github_oidc`、`password`——加第二个插件验证了"前端零改动"的插件化设计

### 测试

- `go test ./...`：**5 包 48 个测试函数全绿，0 失败**，新增：
  - 用户组：成员通过组授权导出、移出成员立即失去访问、删组连带清授权
  - password 插件：正确口令敲门建 pending、错口令/错用户名 403 且不建用户、批准+授权后 Basic 导出、env 格式
  - 插件清单含两种类型 + Secret 字段标记
- **真实二进制 E2E**（curl）：password 实例 → Basic 敲门 403 待批准 → 批准授权 → 导出 `SCRIPT_TOKEN=tok-123` → 错口令/错用户名 403；用户组：建组 → 成员 → 组授权导出 → 移出后空集 → 删组 204
- CI v0.0.2 run ✅（tag + ghcr 镜像）

### 未验证 / 遗留

- 真实 GitHub workflow（M6 部署后）
- 授权（grants）页签仍是占位——授权管理已可在用户详情抽屉和用户组完成，独立"授权"总览页待 M6 或按需
- README 定稿（M6）
- 前端新页面（用户组/设置）未经过真人过目

---

## 2026-11-27 · M3：GitHub OIDC 插件 + /v1/export（MVP 核心）

### 完成

- **插件框架** `internal/app/plugins`：PluginType 契约（Type/Label/Match/ConfigFields/ClaimFields/Parse/Accept）+ 编译期注册表
- **github_oidc 插件**：手写 RS256 验签（零第三方依赖）、JWKS 拉取缓存（TTL 1h、未知 kid 重拉带 10s 节流防放大）、exp/nbf 时钟窗（±60s）、claims 全量字符串化（repository_id 用 json.Number 保精度）、显式拒绝非 RS256 算法
- **实例管理**：providers CRUD + 保存时校验（类型存在 / audience 必填 / 规则可编译）+「试一下」（贴 claims JSON → 逐条规则命中 + iss/aud 模拟核对）
- **认证编排**：MatchTypes → Parse（每类型一次）→ 实例按 id 择优（Accept + 规则 AND）→ 被拒记环形内存（最近 50 条）
- **用户**：`(类型, external_id)` 身份键、首次敲门建 pending、批准/删除 API、last_seen 刷新
- **授权**：CRUD + 创建时校验主体/目标存在（不留孤儿授权）+ 删用户连带清理
- **/v1/export**：认证 → pending 拒绝 → 授权实时展开（个人 ∪ 用户组、组含子孙）→ 解密 → JSON / env 两种格式、`?group=` 过滤、含换行的值拒绝 env 格式（防静默截断）
- **前端**：Provider 实例页（动态表单 + 规则编辑器 + 试一下）、用户页（待批准置顶/批准/详情授权管理/被拒摘要）
- **工具**：`tools/mintjwt` 本地假 OIDC 签发者 + 铸 token（联调用）

### 测试

- `go test ./...`：**5 包全绿，45 个测试函数，0 失败**
- 新增核心覆盖（export_test.go，用 httptest RSA + 假 JWKS + 真签名的 JWT）：
  - 全量导出 / group 过滤 / env 格式 / no-store
  - 无 Bearer、垃圾 token、alg=none、错误签名、过期 token → 401
  - aud 不匹配、准入规则不匹配、PR 事件 → 403，且 **aud 不匹配不建用户**
  - 多实例择优（第一个 aud 不对、第二个对 → 选中第二个）
  - 删授权 → 空集；删用户 → 重建 pending → 403
  - 实例校验（未知类型/缺 audience/规则不可编译 → 400）；试一下两端
- **真实二进制 E2E**（mintjwt 假签发者 + curl）：

| 步骤 | 结果 |
|---|---|
| 建实例（issuer 指假签发者 + owner/event 两规则） | 201 |
| valid 敲门 | 403 身份待批准（pending 建立量 1） |
| 批准 + 授权 minigames 整组 | 200 |
| 导出 | `{"secrets":{"OSS_KEY":"AKIA-E2E-LOCAL"}}` |
| env 格式 / group 过滤 | `OSS_KEY=AKIA-E2E-LOCAL` / 子组正确 |
| 删授权后立即导出 | 空集（实时生效） |
| wrong_owner / wrong_aud / pr_event | 403 身份未被接受 |
| expired | 401 令牌无效 |
| 被拒记录接口 | 显示逐条原因 |

### CI 首跑（上一轮推送触发，run 45940033572）

- ✅ success：go vet + test → tag **v0.0.1** → docker build → ghcr 推送
- ghcr 包详情查询需要 read:packages scope（token 没有），以 workflow 步骤成功为证据

### 未验证 / 遗留

- **真实 GitHub workflow**（token.actions.githubusercontent.com 真令牌）：服务未公网部署，待 M6 部署到 VPS 后做——这是 M3 验收的最后一块
- 用户组管理 UI（M4）、账号密码插件（M5）、设置页、README 定稿（M6）
- 前端 Provider/用户页的视觉细节未经真人过目，`scripts\dev.bat` 起来后自己点一遍

---

## 2026-11-27 · M1+M2：骨架、加密、密钥管理后台

### 完成

- **Go 后端**（模块 `github.com/lyzz0612/deploy-center`，仅 2 个第三方依赖：`modernc.org/sqlite`、`golang.org/x/crypto`）
  - `internal/domain`：naming（两级路径/密钥名校验）、rule（准入正则：AND/整串锚定/缺字段不匹配/保存时编译）、authz（授权并集展开，M4 接线）
  - `internal/infra/crypto`：HKDF-SHA256 派生 + AES-256-GCM，AAD 用每行随机 salt（改名不影响解密）
  - `internal/infra/sqlite`：八张表 schema v1、`PRAGMA user_version` 迁移、PRAGMA 走 DSN（防连接重建后 foreign_keys 失效）、单连接钉死
  - `internal/app`：SecretService（增改删/reveal）、LoginService（单口令 + HMAC 会话）、EnsureSentinel（启动自检，密钥不对拒绝启动）
  - `internal/httpapi`：healthz、登录/登出、分组 CRUD、密钥 CRUD + reveal、限流（10 次/分/IP）、Origin 校验、no-store、静态托管
- **前端**（Vite + React 19 + Tailwind 4 + Radix）：登录页、侧栏布局（工业仪表盘风：近黑底/酸绿强调/等宽）、总览大数字、密钥管理页（分组树/表格/眼睛回显/复制/抽屉增改/删除确认）
- **配套**：`scripts/dev.bat`、多阶段 Dockerfile（distroless）、compose、`release.yml`（push main → 测试 → tag patch+1 → ghcr）
- **二进制 12.2 MB**（含内嵌前端），JS 89.8 KB gzip + CSS 5.8 KB gzip

### 测试

- `go test ./...`：**5 包全绿，40 个测试函数（含子测试共 123 项 RUN/PASS 断言组），0 失败**
- 覆盖：准入正则锚定/AND/缺失/非法、授权并集、路径与密钥名校验边界、加解密 roundtrip/错 AAD/错密钥/篡改/tamper、迁移幂等、级联删除（父组/单密钥连带清授权）、哨兵（换密钥拦截）、登录（错口令/过期/伪造/换口令全下线）、限流 429、跨站 Origin 403、API no-store、列表无明文、SPA 与重定向

### 本地端到端（curl 实测）

| # | 验证项 | 结果 |
|---|---|---|
| 1 | `/healthz` | 200，版本/schema 正确 |
| 2 | 未登录访问 API | 401 |
| 3 | 错口令 ×1、登录限流 | 401 / 429 |
| 4 | 登录→建组→存密钥 | 全通 |
| 5 | 列表不含明文 | ✓（只有 key/时间） |
| 6 | reveal 中文值（UTF-8 字节级） | ✓ roundtrip=True |
| 7 | PUT 改值→reveal | ✓ |
| 8 | 删密钥→reveal | 204→404 |
| 9 | **重启进程后 reveal**（持久化+sentinel） | ✓ |
| 10 | **换 MASTER_KEY 启动** | 拒绝启动，错误信息明确，exit=1 |
| 11 | SPA `/admin/` | 200 无重定向（修掉一个 FileServer 死循环 bug） |
| 12 | hash 资产缓存头 | immutable 1 年 |
| 13 | `-healthcheck` 子命令 | exit=0 |

### 过程中修掉的 bug

1. **SPA 死循环**：index.html 交给 `http.FileServer` 会触发 `/index.html → ./` 规范化重定向，`/admin/` 无限 301。改为 index 手动写响应。
2. **PRAGMA 时效**：`foreign_keys` 是每连接生效，改走 DSN `_pragma` 参数，连接重建不再失效。
3. **AAD 设计缺陷**（规划阶段发现）：AAD 绑 `group.path` 会在改名后解不开，改为每行随机 salt。

### 未验证 / 遗留

- Docker 镜像构建：本机无 docker，**由 CI（release.yml）首次 push 时验证**——若失败按 Actions 日志修
- ghcr 推送、tag 自动 +1：同上，首次 push 验证
- M3（GitHub OIDC 插件 + `/v1/export` + 试一下）、M4（用户/授权）、M5（Provider 实例 + 账号密码插件）、M6（部署 + README 定稿）待做
- 前端「授权/用户/用户组/Provider/设置」页为占位，M3-M5 逐个点亮

### 环境

- Go 1.27.4 zip 绿色安装到 `D:\Tools\go`（winget MSI 报 1603，权限问题），用户 PATH 已加
- Node 24.19.0 / npm 11.17.0
- npm 的 allow-scripts 拦了 esbuild postinstall，手动 `node node_modules/esbuild/install.js`（CI 无此问题）

---

## 2026-11-27 · 两跳换票 + appkey 内置用户 + guest/normal 分级

### 需求（用户裁定）

1. 统一两跳：所有用户先 `POST /v1/auth` 凭凭证换短期 token（默认 2 分钟，设置页可调），再 `GET /v1/export` 拿密钥——包括 GitHub OIDC
2. appkey 成为内置用户类型；password 从「实例」模型改为内置用户类型（不能也不需要建 Provider 实例）；后台「新建用户」只能选这两种，实例型用户由准入自动登记
3. 用户分级：默认一律 `guest`（只能拿 key 名，导出值为固定 `fake_value`，JSON 带 `masked: true`）；`normal` 拿真实值，用户页一键切换
4. 无存量 password 实例，不做迁移

### 改动（后端）

- **插件契约**：`PluginType` 加 `Builtin() bool` 与 `FindUser(ctx, UserFinder, parsed)`；内置型的 Accept 收用户行凭证（password 的比对逻辑原样保留，只是 cfg 来源从实例换成用户行）；`UserFinder` 接口由 app 层适配 Repo（ErrNotFound → nil）
- **appkey 插件**（新包）：`X-App-Key` 头独立于 Bearer/Basic 井水不河水；`ak_`+32B base64；认证 = sha256(appkey) 命中 `users.cred_lookup` 唯一索引（命中即通过，无需解密比对）
- **TokenService**：`v1.<uid>.<exp>.<sig>`，HMAC key = HKDF(MASTER_KEY, "secret-gate/v1-token")（与 admin session 派生域分开）；无状态、过期即吊销、用户删则票失效
- **IdentityService**：认证链拆两形态——内置型 FindUser+凭证比对直接命中用户行；实例型保持 Parse+实例遍历；`ResolveUser` 对内置型直返；自动登记一律 `role=guest`
- **ExportService**：guest 不解密（key 名照列、值全 `fake_value`），审计日志带 role/masked；env 格式 guest 加首行注释 `# guest: values are masked`
- **UserService**：`Create` 重写（type/external_id/password，appkey 自动生成返回明文）、`SetRole`、`RevealCredential`、`RotateCredential`（appkey 新随机、password 可自定义或随机，旧值立即失效）
- **凭证存储**：AES-GCM 加密挂用户行（`cred_cipher/nonce/salt`），AAD=随机盐；`cred_lookup`=sha256 做索引；告别人在 `providers.config_json` 里躺明文密码的时代
- **SettingsService**（新）：`token_ttl_minutes` 存 meta 表，clamp 1–1440，改完对新车立即生效
- **schema v2**：users 加 role + 5 个 cred 列 + `idx_users_cred_lookup` 部分唯一索引；Provider 创建拒绝内置类型；`/v1/auth` 限流（与登录共用 limiter）+ 失败统一「凭证无效」防撞库

### 改动（前端）

- Users 页：新建表单只列内置类型（password 出口令框，appkey 自动生成）；创建成功当场展示凭证+复制按钮；列表加 role 徽标一键切换；详情抽屉凭证查看/轮换
- Providers 页：新建实例类型下拉过滤 `builtin`
- Settings 页：token 寿命编辑 + 两跳 curl 速查（原「账号口令实例」指引同步改掉）
- selftest.yml：改成两跳（换票→导出），新增 `masked=true → 提级 normal` 的失败分支

### 验证

- 5 包 55 个测试函数连跑两轮全绿（新增 AppKeyFlow / PasswordUserFlow / TokenTTLSetting / V1Auth_PluginFailures / TokenFailures / RateLimit / TokenService 单测）
- 真二进制 E2E（curl 实测）：建 appkey 用户→guest 换票（expires_in=120）→导出 `masked:true`+`fake_value`（env 带注释行）→提级 normal→真值→reveal/rotate（旧 key 403 新 key 200）→password 用户 Basic 换票→错口令 403→TTL 改 5 分钟→`expires_in=300`→password 建实例被拒 400

### 已知取舍

- 「预配置 github_oidc 用户」能力随旧 Create 一起移除：实例型用户只能等首次敲门自动登记（首跑无授权空集，补授权重跑即可）——selftest workflow 的指引文案已同步
- 工作区遗留：根目录 dev.bat 与 scripts/dev.bat 在 HEAD 里并存（历史提交所致），本次顺手删根目录那份（scripts\dev.bat 为唯一入口），单独提交

---

## 2026-11-27 · 导出改数组形态（跨组同名 key 共存）

### 缺陷（用户发现）

导出是 `map[key]value`，跨组同名 key（如 prod/dev 各一条 `API_KEY`）在 map 迭代的随机顺序里**静默丢一条**，且每次结果不同。

### 改动（用户拍板：JSON 数组 + env 撞名 422）

- `Export` 返回 `[]SecretEntry{Group, Key, Value}`（group path + key 字典序，确定性）；单条授权与组授权指向同一条密钥时按 `(group, key)` 去重，**同名跨组保留**
- guest：数组照列（value 全 fake_value，masked:true）——「每个组有哪些 key」的完整清单，探测语义更完整
- env 格式保持 `KEY=VALUE`，跨组同名 → 422 点名冲突分组（沿用「含换行值拒绝」的先例：env 装不下就明说）；`?group=` 过滤可消解
- 空集 JSON 由 `{}` 变 `[]`（make 兜底，不会是 null）
- selftest.yml 的 jq 取法改数组（`.secrets | map("\(.group)/\(.key)")`）

### 验证

- 新增 `TestExport_CrossGroupDup`：JSON 两条共存 + 排序、env 422 点名、`?group=` 消解、guest 完整清单两条假值
- 全部用例空集断言 `{}` → `[]`；5 包 56 个测试函数全绿

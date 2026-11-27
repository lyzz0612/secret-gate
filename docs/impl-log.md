# 实施日志

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

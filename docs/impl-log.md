# 实施日志

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

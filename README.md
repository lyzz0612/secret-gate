# Secret Gate（仓库名 deploy-center）

自托管的轻量密钥服务：GitHub Actions 凭一次性 OIDC 令牌取走被授权的密钥，**仓库里不再存任何长期凭据**。Infisical 的个人版：砍掉多租户 / RBAC / 审批流，保留 OIDC 短令牌、加密入库、按身份授权。

## 它解决什么

- 密钥散在各个仓库的 `secrets.*` 里，轮换要挨个仓库点 → 集中一处，改一次全员生效
- workflow 里的长期 PAT / CI key 泄了只能手工吊销 → OIDC 令牌 5 分钟自灭，且只绑指定仓库 / 分支 / 事件
- 担心数据库文件被拿走 → 密钥 AES-256-GCM 加密后入库，密钥在环境变量里

## 快速开始（Docker）

```yaml
services:
  secret-gate:
    image: ghcr.io/lyzz0612/secret-gate:latest
    restart: unless-stopped
    ports: ["127.0.0.1:8788:8788"]
    environment:
      MASTER_KEY: "<必改：openssl rand -hex 32>"
      ADMIN_PASSWORD: "<必改：后台口令>"
    volumes:
      - ./data:/app/data
    healthcheck:
      test: ["CMD", "/vaultd", "-healthcheck"]
      interval: 30s
      timeout: 3s
      retries: 3
```

详细部署（1Panel 反代 / ghcr 私有镜像处理 / 首次接入闭环）：[docs/deploy.md](docs/deploy.md)。
本地开发：`scripts\dev.bat`（Go 1.27+ / Node 20+，口令 `dev`）。

## 接入第一个仓库

后台建一个 `GitHub Actions (OIDC)` 实例（audience = 你的域名，准入规则限定 owner），然后：

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

首次运行会在后台「用户」页出现待批准身份（`github_oidc:仓库ID`）——批准、授权到密钥组，再跑一次即拿到密钥。非 GitHub 调用方可建 `账号口令` 实例，用 Basic auth 走同一出口。

## 概念（三层）

```
请求 → 插件认证(验签 401) → 实例准入(正则 AND, 403, 不建用户) → 用户(首次=待批准)
     → 授权(个人 ∪ 用户组 → 密钥组[含子孙] / 单条) → 解密返回
```

- **插件类型**（代码）vs **Provider 实例**（数据）：同类型可建多个实例（不同 audience / 准入范围），加新类型 = 一个 Go 包 + 一行注册，后台自动出现配置界面
- **密钥组**两级：`项目/环境`；授权给父组 = 含全部子孙
- **授权每次请求实时计算**：删授权立即生效
- **所有删除都是真删**，无归档无回收站
- 准入规则 = 字段正则：整串锚定、规则间 AND、字段缺失不匹配、保存时编译校验；后台有「试一下」

## 安全边界

- 密钥 AES-256-GCM 加密入库，AAD 每行随机 salt——单独拿走 `app.db` 解不开任何密钥；`MASTER_KEY` 换错服务拒绝启动（哨兵自检）
- 明文只出现在：内存、reveal 接口（点眼睛时）、`/v1/export`（通过认证+准入+授权后）；API 响应 `Cache-Control: no-store`
- 后台单口令（环境变量），改口令 = 改配置重启，旧会话全失效；登录限流每 IP 10 次/分
- **不防**：主机被 root、进程内存被 dump、同时拿到 db 和环境变量的人
- 备份 `app.db` 必须与当时的 `MASTER_KEY` 配套，分开存

## 开发

```
go test ./...                    # 后端：5 包 48 个测试函数
npm --prefix web run build       # 前端：产物 go:embed 进二进制（约 12.5 MB）
go run ./tools/mintjwt ...       # 本地假 OIDC 签发者，联调 /v1/export
```

技术栈：Go 标准库（`net/http` 路由、AES-GCM、RS256 验签手写）+ 2 个第三方依赖（纯 Go SQLite、x/crypto）；前端 Vite + React 19 + Tailwind 4 + Radix。

**发版**：push 到 `master` → CI 跑测试 → git tag 末位 +1（v0.0.x）→ 构建镜像推 ghcr。文档改动不触发。

进度与设计：[docs/plan.md](docs/plan.md) · 实施日志：[docs/impl-log.md](docs/impl-log.md)

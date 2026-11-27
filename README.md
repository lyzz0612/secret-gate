# Secret Gate（仓库名 deploy-center）

自托管的轻量密钥服务：让 GitHub Actions 凭一次性的 OIDC 令牌取走自己被授权的密钥，**仓库里不再存任何长期凭据**。

> 当前进度：M1+M2（骨架 / 加密 / 密钥管理后台）已完成；GitHub OIDC 插件、用户与授权管理、Provider 实例管理在后续里程碑，见 [docs/plan.md](docs/plan.md)。

## 快速开始（本地）

```
scripts\dev.bat
```

要求：Go 1.27+、Node 20+。启动后访问 http://127.0.0.1:8788/admin ，开发口令 `dev`。

## 快速开始（Docker）

```yaml
services:
  secret-gate:
    image: ghcr.io/lyzz0612/secret-gate:latest
    restart: unless-stopped
    ports: ["127.0.0.1:8788:8788"]
    environment:
      MASTER_KEY: "至少32字节随机串"      # 必改，与 app.db 同等重要
      ADMIN_PASSWORD: "后台口令"          # 必改
    volumes:
      - ./data:/app/data
    healthcheck:
      test: ["CMD", "/vaultd", "-healthcheck"]
```

反代到公网后，`GET /v1/export` 即为 CI 取密钥入口（需 OIDC 插件，M3 提供）。

## 安全边界

- 密钥 AES-256-GCM 加密后入库，主密钥在 `MASTER_KEY` 环境变量——**单独拿走 app.db 解不开任何密钥**
- 后台单口令登录（环境变量），改口令 = 改配置并重启
- 所有删除都是真删，无归档无回收站
- 不防主机被完全攻破（主密钥与数据同机）

## 开发

```
go test ./...          # 后端测试
npm --prefix web run build   # 前端构建（产物 embed 进二进制）
```

push 到 `main` 自动：测试 → git tag 版本号末位 +1 → 构建镜像推 ghcr。

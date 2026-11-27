# 部署（1Panel + Docker + 反向代理）

服务是单容器 + 单文件 SQLite，数据全在编排目录的 `./data` 下。

## 1. 准备目录与编排

在 VPS 上建目录（示例 `/opt/apps/secret-gate`）：

```
secret-gate/
├── docker-compose.yml
└── data/          # app.db 落这里（容器自动创建）
```

`docker-compose.yml`（三个必改值已标注）：

```yaml
services:
  secret-gate:
    image: ghcr.io/lyzz0612/secret-gate:latest
    container_name: secret-gate
    restart: unless-stopped
    ports:
      - "127.0.0.1:8788:8788"          # 只监听本机，由反代暴露
    environment:
      MASTER_KEY: "<必改：至少32字节随机串>"      # openssl rand -hex 32
      ADMIN_PASSWORD: "<必改：后台口令>"
    volumes:
      - ./data:/app/data
    healthcheck:
      test: ["CMD", "/vaultd", "-healthcheck"]
      interval: 30s
      timeout: 3s
      retries: 3
```

## 2. 拉取镜像（ghcr 默认私有，二选一）

**方式 A：把镜像设为 public（推荐，自用单层）**
GitHub → 你的头像 → Packages → `secret-gate` → Package settings → Danger Zone → Change visibility → Public。
之后 `docker pull` 无需登录。

**方式 B：保持私有，登录拉取**
建一个只有 `read:packages` 权限的 PAT，在 VPS 上：

```
echo <PAT> | docker login ghcr.io -u lyzz0612 --password-stdin
docker compose pull
```

## 3. 启动

```
docker compose up -d
docker compose logs -f        # 应看到 "vaultd vX.Y.Z 启动: http://0.0.0.0:8788"
curl http://127.0.0.1:8788/healthz
```

## 4. 1Panel 反向代理

网站 → 创建网站 → 反向代理 → 域名 `secrets.example.com` → 代理到 `http://127.0.0.1:8788`，申请 HTTPS 证书。

注意：**必须 HTTPS**。GitHub Actions 取 OIDC 令牌与导出请求都走公网，明文传输密钥不可接受。

## 5. 首次接入一个仓库（闭环验证）

1. 浏览器打开 `https://secret-gate.skyup.top/admin/`，用 `ADMIN_PASSWORD` 登录
2. **接入 → 新建实例**：类型 `GitHub Actions (OIDC)`（无配置项），准入规则建议三条：
   - `aud` = `^https://secret-gate.skyup.top$`（防令牌重放到别处）
   - `repository_owner` = `^lyzz0612$`
   - `event_name` = `^(push|workflow_dispatch|release)$`
3. **密钥**：建组（如 `minigames/prod`）并录入第一条密钥
4. Actions 页手动跑 **「Secret Gate 自测」** workflow（`.github/workflows/selftest.yml`）——
   它会打 `/healthz`、取真 OIDC 令牌、调 `/v1/export`，按失败信息对号入座
5. 第一次多半是「200 但 0 条」：认证准入都通了，只差授权——**授权**页给 `lyzz0612/secret-gate` 授一个密钥组，再跑一次自测应全绿
6. 其他仓库接入：抄 README 里的 workflow 片段，audience 一致

## 6. 升级

```
docker compose pull
docker compose up -d
```

数据在 `./data/app.db`，不受容器替换影响。版本号在后台「设置」页可见。

**注意**：`MASTER_KEY` 与 `app.db` 是一对，换任何一个都要想清楚——库对不上密钥服务会拒绝启动（哨兵自检）。

## 7. 备份（手工，按规划不做定时）

```
docker exec secret-gate /bin/sh -c 'sqlite3 data/app.db "VACUUM INTO '"'"'data/backup.db'"'"'"' 
```

（容器里没有 sqlite3 的话，直接 `docker cp` 停机拷贝 `app.db` 也可以：WAL 模式下先 `docker compose stop` 再拷。）
备份 `app.db` 时必须同时记下当时的 `MASTER_KEY`，两者分开存，缺一个数据就废。

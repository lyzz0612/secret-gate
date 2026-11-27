@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0.."

where go >nul 2>nul
if errorlevel 1 (
  echo [!] 未找到 go。已安装在 D:\Tools\go 时请把 D:\Tools\go\bin 加入 PATH
  exit /b 1
)
where node >nul 2>nul
if errorlevel 1 (
  echo [!] 未找到 node，请先安装 Node 20+
  exit /b 1
)

if not exist web\node_modules (
  echo [..] 安装前端依赖（首次较慢）
  call npm --prefix web install --no-fund --no-audit
  if errorlevel 1 exit /b 1
)

echo [..] 构建前端
call npm --prefix web run build
if errorlevel 1 exit /b 1

if not exist data mkdir data

REM 开发密钥固定写死：随机生成会导致每次重启数据库解不开
if not defined MASTER_KEY set "MASTER_KEY=dev-only-32-bytes-key-do-not-use-in-prod"
if not defined ADMIN_PASSWORD set "ADMIN_PASSWORD=dev"

echo [ok] 后台: http://127.0.0.1:8788/admin   口令: dev
start "" http://127.0.0.1:8788/admin
go run ./cmd/vaultd -addr 127.0.0.1:8788 -db data/app.db

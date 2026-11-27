@echo off
chcp 65001 >nul
setlocal
title Secret Gate - dev
cd /d "%~dp0.."

where go >nul 2>nul
if errorlevel 1 (
  echo [!] 未找到 go。若已装在 D:\Tools\go，请把 D:\Tools\go\bin 加入 PATH 后重开终端
  pause
  exit /b 1
)
where node >nul 2>nul
if errorlevel 1 (
  echo [!] 未找到 node，请先安装 Node 20+
  pause
  exit /b 1
)

if not exist web\node_modules (
  echo [..] 安装前端依赖（首次较慢）
  call npm --prefix web install --no-fund --no-audit
  if errorlevel 1 ( echo [!] 前端依赖安装失败 & pause & exit /b 1 )
)

echo [..] 构建前端
call npm --prefix web run build
if errorlevel 1 ( echo [!] 前端构建失败 & pause & exit /b 1 )

if not exist data mkdir data

REM 开发主密钥固定写死：随机生成会导致每次重启数据库解不开
if not defined MASTER_KEY set "MASTER_KEY=dev-only-32-bytes-key-do-not-use-in-prod"
if not defined ADMIN_PASSWORD set "ADMIN_PASSWORD=dev"

echo [ok] 后台: http://127.0.0.1:8788/admin   口令: dev   (Ctrl+C 停止)

REM 3 秒后开浏览器，等服务起来；设 SG_NO_BROWSER=1 可跳过（调试用）
if not defined SG_NO_BROWSER start "" cmd /c "ping -n 4 127.0.0.1 >nul & start http://127.0.0.1:8788/admin"

go run ./cmd/vaultd -addr 127.0.0.1:8788 -db data/app.db

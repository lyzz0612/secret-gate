@echo off
chcp 65001 >nul
REM 根目录入口：双击即启动本地开发环境（实际逻辑在 scripts\dev.bat）
call "%~dp0scripts\dev.bat" %*

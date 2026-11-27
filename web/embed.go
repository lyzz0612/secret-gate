// Package web 内嵌前端构建产物（web/dist）。
// dist 至少含 .gitkeep，保证未构建前端时 go build 仍可编译；
// 真实产物由 npm run build 生成后整体替换。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS

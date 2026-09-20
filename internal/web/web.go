package web

import "embed"

// Assets 内嵌前端构建产物（web/dist 目录）。
//
//go:embed assets/*
var Assets embed.FS

package api

import (
	"net/http"
	"path/filepath"
	"regexp"

	"onecloud-panel/internal/scripts"
)

// 仅允许下载约定命名的架构二进制（杜绝路径穿越/任意文件）。
var dlNameRe = regexp.MustCompile(`^onecloud-panel-linux-(armv7|arm64|amd64|386)$`)

// GET /install.sh — 下发一键安装脚本（公开）。
func (a *API) installScript(w http.ResponseWriter, r *http.Request) {
	b, err := scripts.InstallScript()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "安装脚本不可用")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// GET /dl/{name} — 从发布目录下载对应架构二进制（公开）。
func (a *API) downloadBinary(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !dlNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "非法的下载文件名")
		return
	}
	if a.releaseDir == "" {
		writeError(w, http.StatusNotFound, "发布目录未配置")
		return
	}
	p := filepath.Join(a.releaseDir, filepath.Base(name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, p)
}

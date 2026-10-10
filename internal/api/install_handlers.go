package api

import (
	"net/http"
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

// GET /dl/{name} — 下发对应架构二进制（公开）。
// 面板本机架构直接下发自身；其他架构优先取本地发布目录，缺失时从在线 Release 拉取转发。
func (a *API) downloadBinary(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !dlNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "非法的下载文件名")
		return
	}
	a.serveReleaseBinary(w, r, name)
}

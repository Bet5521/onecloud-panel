package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"onecloud-panel/internal/system"
	"onecloud-panel/internal/update"
	"onecloud-panel/internal/version"
)

// assetArchFor 把节点架构标识（uname -m / normalizeArch 风格）归一到发布产物后缀。
// 返回空串表示无对应发布产物（不支持该架构）。
func assetArchFor(arch string) string { return system.AssetArch(arch) }

// releaseAssetName 由节点架构计算 Linux 发布产物名（与 internal/update.AssetName 约定一致）。
func releaseAssetName(arch string) string {
	if a := assetArchFor(arch); a != "" {
		return "onecloud-panel-linux-" + a
	}
	return ""
}

// serveOwnExecutable 下发面板自身可执行文件（仅当目标架构与面板架构一致时才正确）。
// 返回是否成功开始写出。
func (a *API) serveOwnExecutable(w http.ResponseWriter) bool {
	exe, err := os.Executable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "定位二进制失败")
		return false
	}
	f, err := os.Open(exe)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取二进制失败")
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取二进制信息失败")
		return false
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.Header().Set("Content-Disposition", "attachment; filename=\"onecloud-panel\"")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-OCP-Binary-Source", "self")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("二进制下发写出失败: %v", err)
	}
	return true
}

// serveReleaseBinary 按发布产物名下发对应架构的面板二进制，三级回退：
//  1. 目标架构 == 面板自身架构 → 直接下发当前可执行文件（免网络）；
//  2. 本地发布目录已存在该产物 → 直接下发（免网络）；
//  3. 从在线 Release 拉取对应 tag 的资产并转发（跨架构场景）；
//     若配置了发布目录，转发成功后回填缓存，后续请求即走本地。
func (a *API) serveReleaseBinary(w http.ResponseWriter, r *http.Request, name string) {
	want := strings.TrimPrefix(name, "onecloud-panel-linux-")
	if want == assetArchFor(runtime.GOARCH) {
		if a.serveOwnExecutable(w) {
			return
		}
		return
	}
	if a.releaseDir != "" {
		p := filepath.Join(a.releaseDir, filepath.Base(name))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-OCP-Binary-Source", "local")
			http.ServeFile(w, r, p)
			return
		}
	}
	a.streamReleaseBinary(w, r, name)
}

// defaultReleaseFetch 查询用于下发异构架构二进制的 Release：
// 优先取与面板自身版本一致的 tag，失败（含开发构建）时退化为最新 Release。
func defaultReleaseFetch(ctx context.Context, proxy string) (*update.Release, error) {
	if rel, err := update.FetchByTag(ctx, proxy, version.Version); err == nil {
		return rel, nil
	}
	return update.FetchLatest(ctx, proxy)
}

// streamReleaseBinary 从在线 Release 拉取指定产物并转发给客户端，同时按需回填发布目录。
func (a *API) streamReleaseBinary(w http.ResponseWriter, r *http.Request, name string) {
	proxy, _, _ := a.store.GetSetting("github_proxy")
	fetch := a.releaseFetch
	if fetch == nil {
		fetch = defaultReleaseFetch
	}
	rel, err := fetch(r.Context(), proxy)
	if err != nil {
		writeError(w, http.StatusBadGateway,
			"本地无该架构二进制，且在线 Release 查询失败："+err.Error())
		return
	}
	asset := update.FindAsset(rel, name)
	if asset == nil {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("本地与在线 Release %s 均无 %s", rel.TagName, name))
		return
	}
	body, size, err := update.OpenAsset(r.Context(), proxy, asset)
	if err != nil {
		writeError(w, http.StatusBadGateway, "在线 Release 资产下载失败："+err.Error())
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(name)+"\"")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-OCP-Binary-Source", "release:"+rel.TagName)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)

	// 边转发边落盘缓存；仅在完整下载后才提交（原子 rename），避免半成品。
	var dst io.Writer = w
	var tmp *os.File
	if a.releaseDir != "" {
		if f, err := os.CreateTemp(a.releaseDir, "."+filepath.Base(name)+".part-"); err == nil {
			tmp = f
			dst = io.MultiWriter(w, f)
		}
	}
	n, err := io.Copy(dst, body)
	if tmp != nil {
		cerr := tmp.Close()
		if err == nil && cerr == nil && (size <= 0 || n == size) {
			_ = os.Chmod(tmp.Name(), 0o755)
			if rerr := os.Rename(tmp.Name(), filepath.Join(a.releaseDir, filepath.Base(name))); rerr == nil {
				log.Printf("已缓存 Release 资产 %s（%s，%d 字节）", name, rel.TagName, n)
			} else {
				_ = os.Remove(tmp.Name())
			}
		} else {
			_ = os.Remove(tmp.Name())
		}
	}
	if err != nil {
		log.Printf("在线 Release 二进制转发中断（%s）: %v", name, err)
	}
}

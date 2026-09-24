package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/version"
)

// downloadBackup GET /api/panel/backup — 导出面板数据备份（tar.gz）。
//
// 内容：
//   - panel.db        —— SQLite 一致快照（VACUUM INTO），可直接用于恢复
//   - manifest.json   —— 版本与生成时间
//   - secret.key      —— 仅当 include_secrets=1 时包含（解密节点 Token / SSH 凭据所需）
//
// secret.key 一旦泄露，持有备份者即可解密节点凭据，因此默认不打包，
// 需要完整可恢复备份时显式加 include_secrets=1 并妥善保管归档文件。
func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if a.dataDir == "" {
		writeError(w, http.StatusInternalServerError, "数据目录未配置")
		return
	}
	includeSecrets := r.URL.Query().Get("include_secrets") == "1"

	snap, err := a.dbSnapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "数据库快照失败: "+err.Error())
		return
	}
	defer func() {
		_ = os.RemoveAll(filepath.Dir(snap))
	}()

	var secret []byte
	if includeSecrets {
		secret, err = os.ReadFile(filepath.Join(a.dataDir, "secret.key"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "读取 secret.key 失败（本机可能未初始化密钥盒）")
			return
		}
	}

	name := "onecloud-panel-backup-" + time.Now().Format("20060102-150405") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	add := func(name string, mode int64, open func() (io.ReadCloser, error), size int64) error {
		hdr := &tar.Header{
			Name:    name,
			Mode:    mode,
			Size:    size,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		rc, err := open()
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = io.Copy(tw, rc)
		return err
	}

	manifest := map[string]any{
		"product":             "onecloud-panel",
		"version":             version.Version,
		"created_at":          time.Now().Format(time.RFC3339),
		"includes_secret_key": includeSecrets,
		"restore_hint":        "恢复：停止面板 → 解包到数据目录覆盖 panel.db（含 secret.key 时一并覆盖）→ 启动面板",
	}
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	if err := add("manifest.json", 0o644,
		func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(mb)), nil
		}, int64(len(mb))); err != nil {
		return
	}

	if err := add("panel.db", 0o600,
		func() (io.ReadCloser, error) { return os.Open(snap) },
		fileSize(snap)); err != nil {
		return
	}

	if includeSecrets && len(secret) > 0 {
		_ = add("secret.key", 0o600,
			func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(secret)), nil
			}, int64(len(secret)))
	}

	a.audit.Record(r, "settings", "backup_export", "panel", strconv.FormatInt(time.Now().Unix(), 10),
		audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"includes_secret_key": includeSecrets}))
}

// dbSnapshot 用 VACUUM INTO 生成数据库一致快照，返回快照文件路径。
func (a *API) dbSnapshot() (string, error) {
	dir, err := os.MkdirTemp("", "ocp-backup-*")
	if err != nil {
		return "", err
	}
	snap := filepath.Join(dir, "panel.db")
	if _, err := a.store.DB.Exec(`VACUUM INTO ?`, snap); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("VACUUM INTO 失败: %w", err)
	}
	return snap, nil
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

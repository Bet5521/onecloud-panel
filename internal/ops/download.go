package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProgressFunc 下载进度回调（累计字节）。
type ProgressFunc func(written int64)

// Download 流式下载到 dest 的同目录临时文件，校验 sha256（可空），原子改名并赋权。
func Download(ctx context.Context, url, dest string, mode os.FileMode,
	sha256Hex string, progress ProgressFunc) error {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	h := sha256.New()
	var written int64
	rdr := io.TeeReader(resp.Body, h)
	written, err = io.Copy(f, rdr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if progress != nil {
		progress(written)
	}

	if sha256Hex != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, sha256Hex) {
			_ = os.Remove(tmp)
			return fmt.Errorf("SHA256 校验失败：期望 %s，实际 %s", sha256Hex, got)
		}
	}

	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

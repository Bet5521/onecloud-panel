package executor

import (
	"context"
	"io"
	"os"

	"onecloud-panel/internal/ops"
)

// Downloader 可选能力：下载文件到节点。
type Downloader interface {
	Download(ctx context.Context, url, dest string, mode os.FileMode,
		sha256Hex string, progress io.Writer) error
}

// HealthChecker 可选能力：节点本机健康探测。
type HealthChecker interface {
	Healthcheck(ctx context.Context, kind string, port int, path string) (bool, string)
}

// Download 本机下载（走 ops 共享实现）。
func (l *Local) Download(ctx context.Context, url, dest string, mode os.FileMode,
	sha256Hex string, progress io.Writer) error {
	if l.guard != nil {
		if err := l.guard.Check(dest); err != nil {
			return err
		}
	}
	var pf ops.ProgressFunc
	if progress != nil {
		pf = func(n int64) {}
	}
	return ops.Download(ctx, url, dest, mode, sha256Hex, pf)
}

// Healthcheck 本机健康检查。
func (l *Local) Healthcheck(ctx context.Context, kind string, port int, path string) (bool, string) {
	return ops.Healthcheck(ctx, kind, port, path)
}

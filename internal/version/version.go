package version

import "fmt"

// 通过 -ldflags "-X onecloud-panel/internal/version.Version=..." 注入。
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Print() string {
	return fmt.Sprintf("OneCloud Panel %s (commit: %s, built: %s)", Version, Commit, BuildDate)
}

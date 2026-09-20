package executor

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Guard 路径白名单守卫：访问路径必须落在允许根目录之下。
type Guard struct {
	roots []string
}

// NewGuard 创建守卫，roots 为允许的绝对路径目录/文件。
func NewGuard(roots ...string) *Guard {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		if abs, err := filepath.Abs(r); err == nil {
			out = append(out, abs)
		}
	}
	return &Guard{roots: out}
}

// AddRoot 追加允许根。
func (g *Guard) AddRoot(root string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return
	}
	g.roots = append(g.roots, abs)
}

// Check 校验路径：必须为绝对路径、已清洗，且位于某个允许根之下。
func (g *Guard) Check(path string) error {
	if len(g.roots) == 0 {
		return fmt.Errorf("白名单为空，拒绝访问: %s", path)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("路径必须为绝对路径: %s", path)
	}
	p := filepath.Clean(path)
	if strings.Contains(p, "..") {
		return fmt.Errorf("非法路径: %s", path)
	}
	for _, root := range g.roots {
		if p == root {
			return nil
		}
		if strings.HasPrefix(p, root+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("路径不在白名单内: %s", path)
}

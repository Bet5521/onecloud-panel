package executor

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Guard 路径白名单守卫：访问路径必须落在允许根目录之下。
type Guard struct {
	roots     []string
	realRoots []string // 解析符号链接后的根（与 roots 一一对应）
}

// NewGuard 创建守卫，roots 为允许的绝对路径目录/文件。
func NewGuard(roots ...string) *Guard {
	g := &Guard{}
	for _, r := range roots {
		g.AddRoot(r)
	}
	return g
}

// AddRoot 追加允许根。
func (g *Guard) AddRoot(root string) {
	if root == "" {
		return
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return
	}
	g.roots = append(g.roots, abs)
	real := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		real = r
	}
	g.realRoots = append(g.realRoots, real)
}

// Check 校验路径：必须为绝对路径、已清洗，且位于某个允许根之下。
// 额外做符号链接解析：/data/x 本身合规但若它是指向 /etc/shadow 的软链，
// 直接放行就会绕过白名单（读写会跟随软链）。
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
	// 解析符号链接（目标不存在时回退到已存在的父目录）
	if real, err := evalPath(p); err == nil {
		p = real
	}
	for _, root := range g.realRoots {
		if p == root {
			return nil
		}
		if strings.HasPrefix(p, root+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("路径不在白名单内: %s", path)
}

// evalPath 解析路径中的符号链接；路径本身不存在时逐级回退到存在的父目录再拼回。
func evalPath(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	parent := filepath.Dir(p)
	base := filepath.Base(p)
	realParent, perr := filepath.EvalSymlinks(parent)
	if perr != nil {
		return "", err
	}
	return filepath.Join(realParent, base), nil
}

package recipes

import (
	"embed"
	"io/fs"
)

// Files 内置应用配方目录（YAML）。
//
//go:embed files/*.yaml
var Files embed.FS

// LoadBuiltin 加载内嵌的全部配方。
func LoadBuiltin() (*Registry, error) {
	sub, err := fs.Sub(Files, "files")
	if err != nil {
		return nil, err
	}
	return Load(sub)
}

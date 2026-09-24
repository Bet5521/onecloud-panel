package auth

import (
	"errors"
	"fmt"
	"unicode"
)

// ValidateUsername 用户名字符集校验（长度校验由调用方按各自规则先行处理）。
// 禁止空白/控制字符与引号、路径分隔、shell 元字符等：
// 用户名会出现在审计日志、导出文件与通知文案中，收紧字符集可避免日志注入与视觉欺骗。
func ValidateUsername(name string) error {
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("用户名不能包含空白或控制字符")
		}
		switch r {
		case '"', '\'', '\\', '/', '%', '$', '`', ';', '|', '&', '<', '>', '\n', '\r':
			return fmt.Errorf("用户名包含不允许的字符 %q", r)
		}
	}
	return nil
}

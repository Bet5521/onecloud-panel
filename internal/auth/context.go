package auth

import (
	"context"

	"onecloud-panel/internal/store"
)

type ctxKey int

const identityKey ctxKey = iota

// Identity 当前登录身份。
type Identity struct {
	User     *store.User
	RoleCode string
	Perms    map[string]bool
}

// Has 判断是否拥有权限点。
func (i *Identity) Has(perm string) bool {
	if i == nil {
		return false
	}
	// 管理员始终拥有全部权限（兜底）
	if i.RoleCode == "admin" {
		return true
	}
	return i.Perms[perm]
}

// WithContext 注入身份。
func (i *Identity) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, identityKey, i)
}

// FromContext 读取身份；不存在返回 nil。
func FromContext(ctx context.Context) *Identity {
	i, _ := ctx.Value(identityKey).(*Identity)
	return i
}

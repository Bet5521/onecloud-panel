package store

// 权限点：模块:动作
const (
	PermDashboardRead = "dashboard:read"

	PermNodeRead  = "node:read"
	PermNodeWrite = "node:write"

	PermAppRead  = "app:read"
	PermAppWrite = "app:write"

	PermUserRead  = "user:read"
	PermUserWrite = "user:write"

	PermRoleRead  = "role:read"
	PermRoleWrite = "role:write"

	PermAuditRead = "auditlog:read"

	PermSettingsRead  = "settings:read"
	PermSettingsWrite = "settings:write"
)

// AllPermissions 全部权限点。
var AllPermissions = []string{
	PermDashboardRead,
	PermNodeRead, PermNodeWrite,
	PermAppRead, PermAppWrite,
	PermUserRead, PermUserWrite,
	PermRoleRead, PermRoleWrite,
	PermAuditRead,
	PermSettingsRead, PermSettingsWrite,
}

// defaultRolePermissions 预置角色默认权限。
var defaultRolePermissions = map[string][]string{
	"admin": AllPermissions,
	"operator": {
		PermDashboardRead,
		PermNodeRead, PermNodeWrite,
		PermAppRead, PermAppWrite,
		PermAuditRead,
	},
	"viewer": {
		PermDashboardRead,
		PermNodeRead,
		PermAppRead,
		PermAuditRead,
	},
}

var builtinRoles = []struct {
	code      string
	name      string
	protected bool
}{
	{"admin", "管理员", true},
	{"operator", "操作员", false},
	{"viewer", "只读用户", false},
}

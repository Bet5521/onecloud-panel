package api

import (
	"net/http"

	"onecloud-panel/internal/apps"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/self"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/version"
)

// API 面板 HTTP API 组合根；后续任务在此装配更多模块。
type API struct {
	store      *store.Store
	mw         *auth.Middleware
	authH      *auth.Handler
	audit      *audit.Service
	nodes      *node.Service
	recipes    *recipes.Registry
	tasks      *runner.Runner
	apps       *apps.Manager
	selfSvc    *self.Service
	sessions   *auth.Manager
	releaseDir string
	dataDir    string
	listenAddr string
	resetLimit *auth.LoginLimiter
	codeSink   func(username, code string) // 测试注入：捕获重置码
}

// SetDataDir 注入面板数据目录（TLS 证书文件落盘用）。
func (a *API) SetDataDir(dir string) {
	a.dataDir = dir
}

// SetResetCodeSink 注入重置码回调（仅测试使用）。
func (a *API) SetResetCodeSink(f func(username, code string)) {
	a.codeSink = f
}

// SetSelfService 注入面板自身管理服务。
func (a *API) SetSelfService(s *self.Service) {
	a.selfSvc = s
}

// SetSessionManager 注入会话管理器（初始化完成后自动登录）。
func (a *API) SetSessionManager(m *auth.Manager) {
	a.sessions = m
}

// SetReleaseDir 注入发布二进制目录（供 /dl 下载各架构二进制）。
func (a *API) SetReleaseDir(dir string) {
	a.releaseDir = dir
}

// SetListenAddr 注入面板实际监听地址（Host 头缺端口时据此补全，避免硬编码端口）。
func (a *API) SetListenAddr(addr string) {
	a.listenAddr = addr
}

// New 创建 API 装配器。
func New(s *store.Store, authH *auth.Handler, mw *auth.Middleware, asvc *audit.Service,
	nodes *node.Service, reg *recipes.Registry, taskRunner *runner.Runner,
	appManager *apps.Manager) *API {
	return &API{
		store: s, mw: mw, authH: authH, audit: asvc, nodes: nodes, recipes: reg,
		tasks: taskRunner, apps: appManager,
	}
}

// Handler 构建完整路由。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	// ---- 公开路由（无需登录） ----
	mux.HandleFunc("GET /api/system/status", a.systemStatus)
	mux.HandleFunc("POST /api/setup", a.setup)
	mux.HandleFunc("POST /api/auth/login", a.authH.Login)
	mux.HandleFunc("POST /api/auth/password-reset/request", a.resetRequest)
	mux.HandleFunc("POST /api/auth/password-reset/confirm", a.resetConfirm)
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": version.Version})
	})

	// ---- Agent 注册/心跳（Token 自身鉴权，无会话） ----
	mux.HandleFunc("POST /api/agent/register", a.agentRegister)
	mux.HandleFunc("POST /api/agent/heartbeat", a.agentHeartbeat)

	// ---- 一键安装（公开） ----
	mux.HandleFunc("GET /install.sh", a.installScript)
	mux.HandleFunc("GET /dl/{name}", a.downloadBinary)

	// ---- 节点管理 ----
	mux.Handle("GET /api/nodes", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeRead, http.HandlerFunc(a.listNodes))))
	mux.Handle("GET /api/nodes/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeRead, http.HandlerFunc(a.getNode))))
	mux.Handle("POST /api/nodes/manual", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.manualAddNode))))
	mux.Handle("POST /api/nodes/ssh-install", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.sshInstallNode))))
	// 进度端点用查询参数承载任务 ID：路径式 /api/nodes/ssh-install/{id} 会与
	// /api/nodes/{id}/info 等模式冲突导致 ServeMux 注册 panic。
	mux.Handle("GET /api/nodes/ssh-install", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.sshInstallProgress))))
	mux.Handle("PUT /api/nodes/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.updateNode))))
	mux.Handle("DELETE /api/nodes/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.deleteNode))))
	mux.Handle("POST /api/nodes/{id}/rotate-token", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.rotateNodeToken))))
	mux.Handle("GET /api/nodes/{id}/info", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeRead, http.HandlerFunc(a.nodeLiveInfo))))
	mux.Handle("GET /api/nodes/{id}/docker/status", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeRead, http.HandlerFunc(a.dockerStatus))))
	mux.Handle("POST /api/nodes/{id}/docker/install", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.installDocker))))
	mux.Handle("PUT /api/nodes/{id}/docker-config", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.updateNodeDockerConfig))))
	mux.Handle("POST /api/nodes/{id}/docker/apply-config", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.applyNodeDockerConfig))))
	mux.Handle("GET /api/network-suggest", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeRead, http.HandlerFunc(a.suggestNetwork))))
	mux.Handle("GET /api/registration-tokens", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeRead, http.HandlerFunc(a.listTokens))))
	mux.Handle("POST /api/registration-tokens", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.createToken))))
	mux.Handle("DELETE /api/registration-tokens/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermNodeWrite, http.HandlerFunc(a.deleteToken))))

	// ---- 需要登录 ----
	mux.Handle("POST /api/auth/logout", a.mw.RequireAuth(http.HandlerFunc(a.authH.Logout)))
	mux.Handle("GET /api/auth/me", a.mw.RequireAuth(http.HandlerFunc(a.authH.Me)))

	mux.Handle("GET /api/panel/info", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsRead, a.panelInfo)))
	mux.Handle("GET /api/panel/status", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsRead, http.HandlerFunc(a.panelStatus))))
	mux.Handle("GET /api/panel/journal", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsRead, http.HandlerFunc(a.panelJournal))))
	mux.Handle("POST /api/panel/restart", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.panelRestart))))
	mux.Handle("GET /api/settings", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsRead, a.getSettings)))
	mux.Handle("PUT /api/settings", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, a.updateSettings)))
	mux.Handle("POST /api/settings/tls", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.updateTLSSettings))))
	mux.Handle("POST /api/settings/smtp-test", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.testSMTP))))

	// ---- 通知管理 ----
	mux.Handle("GET /api/notifications/channels", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsRead, http.HandlerFunc(a.listNotificationChannels))))
	mux.Handle("POST /api/notifications/channels", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.createNotificationChannel))))
	mux.Handle("PUT /api/notifications/channels/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.updateNotificationChannel))))
	mux.Handle("POST /api/notifications/channels/{id}/test", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.testNotificationChannel))))
	mux.Handle("DELETE /api/notifications/channels/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermSettingsWrite, http.HandlerFunc(a.deleteNotificationChannel))))

	mux.Handle("GET /api/audit-logs", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAuditRead, audit.NewHandler(a.audit).List)))

	// ---- 仪表盘 ----
	mux.Handle("GET /api/dashboard/summary", a.mw.RequireAuth(
		auth.RequirePermission(store.PermDashboardRead, http.HandlerFunc(a.dashboardSummary))))

	// ---- 用户管理 ----
	mux.Handle("GET /api/users", a.mw.RequireAuth(
		auth.RequirePermission(store.PermUserRead, http.HandlerFunc(a.listUsers))))
	mux.Handle("POST /api/users", a.mw.RequireAuth(
		auth.RequirePermission(store.PermUserWrite, http.HandlerFunc(a.createUser))))
	mux.Handle("PUT /api/users/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermUserWrite, http.HandlerFunc(a.updateUser))))
	mux.Handle("DELETE /api/users/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermUserWrite, http.HandlerFunc(a.deleteUser))))
	mux.Handle("POST /api/users/{id}/password", a.mw.RequireAuth(
		auth.RequirePermission(store.PermUserWrite, http.HandlerFunc(a.resetUserPassword))))
	mux.Handle("POST /api/users/{id}/super-code", a.mw.RequireAuth(
		auth.RequirePermission(store.PermUserWrite, http.HandlerFunc(a.regenerateSuperCode))))
	mux.Handle("PUT /api/auth/profile", a.mw.RequireAuth(
		http.HandlerFunc(a.updateMyProfile)))
	mux.Handle("GET /api/auth/notify-channels", a.mw.RequireAuth(
		http.HandlerFunc(a.listMyNotifyChannels)))
	mux.Handle("POST /api/auth/change-password", a.mw.RequireAuth(
		http.HandlerFunc(a.changePassword)))

	// ---- 角色管理 ----
	mux.Handle("GET /api/roles", a.mw.RequireAuth(
		auth.RequirePermission(store.PermRoleRead, http.HandlerFunc(a.listRoles))))
	mux.Handle("PUT /api/roles/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermRoleWrite, http.HandlerFunc(a.updateRole))))

	// ---- 应用配方 ----
	mux.Handle("GET /api/recipes", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.listRecipes))))
	mux.Handle("GET /api/recipes/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.getRecipe))))

	// ---- 节点应用生命周期 ----
	mux.Handle("GET /api/nodes/{id}/apps", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.listInstallations))))
	mux.Handle("POST /api/nodes/{id}/apps/{app}/install", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.installApp))))
	mux.Handle("POST /api/nodes/{id}/apps/{app}/uninstall", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.uninstallApp))))
	mux.Handle("POST /api/nodes/{id}/apps/{app}/{action}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.appServiceAction))))
	mux.Handle("GET /api/nodes/{id}/apps/{app}/status", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.appStatus))))
	mux.Handle("GET /api/nodes/{id}/apps/{app}/journal", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.appJournal))))
	mux.Handle("GET /api/nodes/{id}/apps/{app}/config", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.readAppConfig))))
	mux.Handle("PUT /api/nodes/{id}/apps/{app}/config", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.writeAppConfig))))

	// ---- 自定义应用（CRUD + 二进制上传；生命周期复用上方节点应用路由，app=custom-<id>） ----
	mux.Handle("GET /api/custom-apps", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.listCustomApps))))
	mux.Handle("POST /api/custom-apps", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.createCustomApp))))
	mux.Handle("GET /api/custom-apps/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.getCustomApp))))
	mux.Handle("PUT /api/custom-apps/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.updateCustomApp))))
	mux.Handle("DELETE /api/custom-apps/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.deleteCustomApp))))
	mux.Handle("POST /api/custom-apps/{id}/binary", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppWrite, http.HandlerFunc(a.uploadCustomBinary))))

	// ---- 后台任务 ----
	mux.Handle("GET /api/tasks", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.listTasks))))
	mux.Handle("GET /api/tasks/{id}", a.mw.RequireAuth(
		auth.RequirePermission(store.PermAppRead, http.HandlerFunc(a.getTask))))

	return audit.RequestIDMiddleware(mux)
}

package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/store"
)

var appIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,32}$`)

type customAppReq struct {
	AppID       string `json:"app_id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	Method      string `json:"method"` // native / docker

	// 通用
	Ports     []recipes.PortSpec `json:"ports"`
	Variables []recipes.Variable `json:"variables"`

	// native
	DownloadURL     string `json:"download_url"`
	UnitName        string `json:"unit_name"`
	UnitTemplate    string `json:"unit_template"`
	InstallScript   string `json:"install_script"`
	UninstallScript string `json:"uninstall_script"`

	// docker
	DockerImage     string   `json:"docker_image"`
	DockerPorts     []string `json:"docker_ports"`
	DockerVolumes   []string `json:"docker_volumes"`
	DockerEnv       []string `json:"docker_env"`
	DockerNetwork   string   `json:"docker_network"`
	DockerRestart   string   `json:"docker_restart"`
	DockerPrivileged bool    `json:"docker_privileged"`

	// 健康检查
	HealthcheckType string   `json:"healthcheck_type"`
	HealthcheckPort int      `json:"healthcheck_port"`
	HealthcheckPath string   `json:"healthcheck_path"`
	HealthcheckCmd  []string `json:"healthcheck_cmd"`
}

// POST /api/custom-apps
func (a *API) createCustomApp(w http.ResponseWriter, r *http.Request) {
	var req customAppReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := validateCustomAppReq(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 检查 app_id 是否与内置配方冲突
	if _, ok := a.recipes.Get(req.AppID); ok {
		writeError(w, http.StatusConflict, "应用标识与内置配方冲突")
		return
	}
	// 检查 app_id 是否已存在
	exists, _ := a.store.CustomAppExists(req.AppID)
	if exists {
		writeError(w, http.StatusConflict, "应用标识已存在")
		return
	}

	user := authUser(r)
	app := buildCustomApp(&req, user.ID)
	id, err := a.store.CreateCustomApp(app)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "custom_app", "create", "custom_app", req.AppID,
		audit.ResultSuccess, audit.DetailJSON(map[string]any{"name": req.Name}))
	app.ID = id
	// 同步到配方注册表
	if err := a.apps.SyncCustomApps(); err != nil {
		writeError(w, http.StatusInternalServerError, "同步自定义应用失败: "+err.Error())
		return
	}
	writeJSON(w, app)
}

// PUT /api/custom-apps/{id}
func (a *API) updateCustomApp(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的 ID")
		return
	}
	existing, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "自定义应用不存在")
		return
	}
	var req customAppReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	// 保持原有 app_id 不变
	req.AppID = existing.AppID
	if err := validateCustomAppReq(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user := authUser(r)
	app := buildCustomApp(&req, user.ID)
	app.ID = id
	app.CreatedBy = existing.CreatedBy
	app.CreatedAt = existing.CreatedAt
	if err := a.store.UpdateCustomApp(app); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "custom_app", "update", "custom_app", req.AppID,
		audit.ResultSuccess, "")
	// 同步到配方注册表
	if err := a.apps.SyncCustomApps(); err != nil {
		writeError(w, http.StatusInternalServerError, "同步自定义应用失败: "+err.Error())
		return
	}
	writeJSON(w, app)
}

// DELETE /api/custom-apps/{id}
func (a *API) deleteCustomApp(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的 ID")
		return
	}
	existing, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "自定义应用不存在")
		return
	}
	if err := a.store.DeleteCustomApp(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "custom_app", "delete", "custom_app", existing.AppID,
		audit.ResultSuccess, "")
	// 同步到配方注册表
	if err := a.apps.SyncCustomApps(); err != nil {
		writeError(w, http.StatusInternalServerError, "同步自定义应用失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET /api/custom-apps
func (a *API) listCustomApps(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListCustomApps()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.CustomApp{}
	}
	writeJSON(w, map[string]any{"items": items, "total": len(items)})
}

// GET /api/custom-apps/{id}
func (a *API) getCustomApp(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的 ID")
		return
	}
	app, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "自定义应用不存在")
		return
	}
	writeJSON(w, app)
}

// validateCustomAppReq 校验自定义应用请求。
func validateCustomAppReq(req *customAppReq) error {
	req.AppID = strings.TrimSpace(req.AppID)
	req.Name = strings.TrimSpace(req.Name)
	req.Method = strings.TrimSpace(req.Method)

	if !appIDPattern.MatchString(req.AppID) {
		return &validationError{"应用标识需为 2-33 位小写字母数字/-_"}
	}
	if req.Name == "" {
		return &validationError{"应用名称不能为空"}
	}
	if req.Method == "" {
		req.Method = "docker"
	}
	if req.Method != "native" && req.Method != "docker" {
		return &validationError{"安装方式仅支持 native 或 docker"}
	}

	// 按方式校验必填字段
	switch req.Method {
	case "native":
		if req.DownloadURL == "" {
			return &validationError{"原生方式需要提供下载地址"}
		}
		if req.UnitName == "" {
			return &validationError{"原生方式需要提供 systemd 服务名"}
		}
	case "docker":
		if req.DockerImage == "" {
			return &validationError{"Docker 方式需要提供镜像名"}
		}
	}

	// 校验端口
	for i, p := range req.Ports {
		if p.Port < 1 || p.Port > 65535 {
			return &validationError{"端口[" + strconv.Itoa(i) + "]越界"}
		}
		if p.Proto != "tcp" && p.Proto != "udp" {
			return &validationError{"端口[" + strconv.Itoa(i) + "]协议仅支持 tcp/udp"}
		}
	}

	// 校验变量
	seen := map[string]bool{}
	for _, v := range req.Variables {
		v.Key = strings.TrimSpace(v.Key)
		if v.Key == "" || !appIDPattern.MatchString("v-"+v.Key) {
			return &validationError{"变量 key 格式不正确: " + v.Key}
		}
		if seen[v.Key] {
			return &validationError{"变量 key 重复: " + v.Key}
		}
		seen[v.Key] = true
	}

	// 校验健康检查
	if req.HealthcheckType != "" {
		switch req.HealthcheckType {
		case "http", "tcp":
			if req.HealthcheckPort < 1 || req.HealthcheckPort > 65535 {
				return &validationError{"健康检查端口非法"}
			}
		case "command":
			if len(req.HealthcheckCmd) == 0 {
				return &validationError{"command 类型健康检查需要提供命令"}
			}
		default:
			return &validationError{"健康检查类型仅支持 http/tcp/command"}
		}
	}

	return nil
}

type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }

// buildCustomApp 从请求构建 CustomApp 模型。
func buildCustomApp(req *customAppReq, userID int64) *store.CustomApp {
	portsJSON, _ := json.Marshal(req.Ports)
	varsJSON, _ := json.Marshal(req.Variables)
	dockerPortsJSON, _ := json.Marshal(req.DockerPorts)
	dockerVolumesJSON, _ := json.Marshal(req.DockerVolumes)
	dockerEnvJSON, _ := json.Marshal(req.DockerEnv)
	hcCmdJSON, _ := json.Marshal(req.HealthcheckCmd)

	return &store.CustomApp{
		AppID:       req.AppID,
		Name:        req.Name,
		Category:    req.Category,
		Icon:        req.Icon,
		Description: req.Description,
		Homepage:    req.Homepage,
		Method:      req.Method,

		Ports:     string(portsJSON),
		Variables: string(varsJSON),

		DownloadURL:     req.DownloadURL,
		UnitName:        req.UnitName,
		UnitTemplate:    req.UnitTemplate,
		InstallScript:   req.InstallScript,
		UninstallScript: req.UninstallScript,

		DockerImage:      req.DockerImage,
		DockerPorts:      string(dockerPortsJSON),
		DockerVolumes:    string(dockerVolumesJSON),
		DockerEnv:        string(dockerEnvJSON),
		DockerNetwork:    req.DockerNetwork,
		DockerRestart:    req.DockerRestart,
		DockerPrivileged: req.DockerPrivileged,

		HealthcheckType: req.HealthcheckType,
		HealthcheckPort: req.HealthcheckPort,
		HealthcheckPath: req.HealthcheckPath,
		HealthcheckCmd:  string(hcCmdJSON),

		CreatedBy: &userID,
	}
}

// authUser 从请求中获取当前用户。
func authUser(r *http.Request) *store.User {
	id := auth.FromContext(r.Context())
	if id != nil && id.User != nil {
		return id.User
	}
	return &store.User{ID: 0}
}
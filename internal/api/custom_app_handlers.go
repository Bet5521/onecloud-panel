package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/store"
)

// customAppDTO 自定义应用视图模型。
type customAppDTO struct {
	ID          int64          `json:"id"`
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	Icon        string         `json:"icon"`
	Description string         `json:"description"`
	Homepage    string         `json:"homepage"`
	Config      map[string]any `json:"config,omitempty"`
	OwnerUserID *int64         `json:"owner_user_id"`
	CreatedBy   *int64         `json:"created_by"`
	CreatedAt   int64          `json:"created_at"`
	UpdatedAt   int64          `json:"updated_at"`
	HasBinary   bool           `json:"has_binary"`
}

type customAppReq struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	Icon        string         `json:"icon"`
	Description string         `json:"description"`
	Homepage    string         `json:"homepage"`
	Config      map[string]any `json:"config"`
	System      bool           `json:"system"` // 仅管理员：true 表示系统级（owner=NULL）
}

func customAppToDTO(c *store.CustomApp, hasBinary bool) customAppDTO {
	d := customAppDTO{
		ID:          c.ID,
		Type:        c.Type,
		Name:        c.Name,
		Category:    c.Category,
		Icon:        c.Icon,
		Description: c.Description,
		Homepage:    c.Homepage,
		OwnerUserID: c.OwnerUserID,
		CreatedBy:   c.CreatedBy,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
		HasBinary:   hasBinary,
	}
	if c.ConfigJSON != "" {
		var m map[string]any
		if json.Unmarshal([]byte(c.ConfigJSON), &m) == nil {
			d.Config = m
		}
	}
	return d
}

// customAppOwns 当前用户是否可管理该自定义应用（管理员或创建者）。
func (a *API) customAppEditable(r *http.Request, c *store.CustomApp) bool {
	if callerIsAdmin(r) {
		return true
	}
	uid, ok := callerID(r)
	if !ok {
		return false
	}
	return c.OwnerUserID != nil && *c.OwnerUserID == uid
}

// GET /api/custom-apps — 自定义应用清单。管理员见全部，普通用户仅见本人创建。
func (a *API) listCustomApps(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	visibleTo := &uid
	if callerIsAdmin(r) || !ok {
		visibleTo = nil // 管理员/未知：全部
	}
	apps, err := a.store.ListCustomApps(visibleTo)
	if err != nil {
		writeError(w, 500, "查询自定义应用失败")
		return
	}
	out := make([]customAppDTO, 0, len(apps))
	for i := range apps {
		hasBin := false
		if apps[i].Type == "binary" && a.dataDir != "" {
			if _, serr := os.Stat(filepath.Join(a.dataDir, "custom-apps",
				strconv.FormatInt(apps[i].ID, 10), "app")); serr == nil {
				hasBin = true
			}
		}
		out = append(out, customAppToDTO(&apps[i], hasBin))
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

// GET /api/custom-apps/{id} — 详情（含完整配置，供编辑回显）。
func (a *API) getCustomApp(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	c, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, 404, "自定义应用不存在")
		return
	}
	hasBin := false
	if c.Type == "binary" && a.dataDir != "" {
		if _, serr := os.Stat(filepath.Join(a.dataDir, "custom-apps",
			strconv.FormatInt(id, 10), "app")); serr == nil {
			hasBin = true
		}
	}
	writeJSON(w, customAppToDTO(c, hasBin))
}

// POST /api/custom-apps — 新建自定义应用。
func (a *API) createCustomApp(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	if !ok {
		writeError(w, 401, "未登录")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "请求读取失败")
		return
	}
	var req customAppReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if req.Type != "github" && req.Type != "docker" && req.Type != "binary" {
		writeError(w, 400, "不支持的应用类型（github/docker/binary）")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, 400, "应用名称必填")
		return
	}
	cfgJSON := "{}"
	if len(req.Config) > 0 {
		if b, err := json.Marshal(req.Config); err == nil {
			cfgJSON = string(b)
		}
	}
	// 合成校验：提前暴露必填/格式错误
	probe := &store.CustomApp{Type: req.Type, Name: req.Name, Category: req.Category,
		Icon: req.Icon, Description: req.Description, Homepage: req.Homepage, ConfigJSON: cfgJSON}
	if _, err := a.apps.SynthRecipe(probe); err != nil {
		writeError(w, 400, "配置无效: "+err.Error())
		return
	}
	owner := &uid
	if callerIsAdmin(r) && req.System {
		owner = nil
	}
	created, err := a.store.CreateCustomApp(&store.CustomApp{
		Type: req.Type, Name: req.Name, Category: req.Category, Icon: req.Icon,
		Description: req.Description, Homepage: req.Homepage, ConfigJSON: cfgJSON,
		OwnerUserID: owner, CreatedBy: &uid,
	})
	if err != nil {
		writeError(w, 500, "创建自定义应用失败")
		return
	}
	if err := a.apps.RegisterCustomApp(created); err != nil {
		writeError(w, 500, "配方注册失败: "+err.Error())
		return
	}
	a.audit.Record(r, "app", "custom_create", "custom_app",
		strconv.FormatInt(created.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"type": created.Type, "name": created.Name}))
	writeJSON(w, customAppToDTO(created, false))
}

// PUT /api/custom-apps/{id} — 更新元信息与配置。
func (a *API) updateCustomApp(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	c, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, 404, "自定义应用不存在")
		return
	}
	if !a.customAppEditable(r, c) {
		writeError(w, 403, "无权修改该应用")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "请求读取失败")
		return
	}
	var req customAppReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = c.Name
	}
	cfgJSON := c.ConfigJSON
	if len(req.Config) > 0 {
		if b, err := json.Marshal(req.Config); err == nil {
			cfgJSON = string(b)
		}
	}
	// 以更新后的内容合成校验
	probe := &store.CustomApp{ID: id, Type: c.Type, Name: name, Category: req.Category,
		Icon: req.Icon, Description: req.Description, Homepage: req.Homepage, ConfigJSON: cfgJSON}
	if _, err := a.apps.SynthRecipe(probe); err != nil {
		writeError(w, 400, "配置无效: "+err.Error())
		return
	}
	if err := a.store.UpdateCustomApp(id, name, req.Category, req.Icon,
		req.Description, req.Homepage, cfgJSON); err != nil {
		writeError(w, 500, "更新自定义应用失败")
		return
	}
	updated, _ := a.store.GetCustomApp(id)
	if updated != nil {
		if err := a.apps.RegisterCustomApp(updated); err != nil {
			writeError(w, 500, "配方重注册失败: "+err.Error())
			return
		}
	}
	a.audit.Record(r, "app", "custom_update", "custom_app",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, customAppToDTO(updated, false))
}

// DELETE /api/custom-apps/{id} — 删除自定义应用（并移除合成配方）。
func (a *API) deleteCustomApp(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	c, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, 404, "自定义应用不存在")
		return
	}
	if !a.customAppEditable(r, c) {
		writeError(w, 403, "无权删除该应用")
		return
	}
	a.apps.UnregisterCustomApp(id)
	if err := a.store.DeleteCustomApp(id); err != nil {
		writeError(w, 500, "删除失败")
		return
	}
	a.audit.Record(r, "app", "custom_delete", "custom_app",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

// POST /api/custom-apps/{id}/binary — 上传二进制文件（仅 binary 类型）。
func (a *API) uploadCustomBinary(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	c, err := a.store.GetCustomApp(id)
	if err != nil {
		writeError(w, 404, "自定义应用不存在")
		return
	}
	if !a.customAppEditable(r, c) {
		writeError(w, 403, "无权操作该应用")
		return
	}
	if c.Type != "binary" {
		writeError(w, 400, "仅二进制类型应用需要上传文件")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil { // 64MB 上限
		writeError(w, 400, "表单解析失败")
		return
	}
	fh, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "未找到文件字段 file")
		return
	}
	defer fh.Close()
	if a.dataDir == "" {
		writeError(w, 500, "数据目录未配置")
		return
	}
	dir := filepath.Join(a.dataDir, "custom-apps", strconv.FormatInt(id, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		writeError(w, 500, "创建目录失败")
		return
	}
	dest := filepath.Join(dir, "app")
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		writeError(w, 500, "写入失败")
		return
	}
	if _, err := io.Copy(out, fh); err != nil {
		out.Close()
		writeError(w, 500, "保存失败")
		return
	}
	out.Close()
	if err := os.Rename(tmp, dest); err != nil {
		writeError(w, 500, "落盘失败")
		return
	}
	a.audit.Record(r, "app", "custom_binary_upload", "custom_app",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok", "path": dest})
}

// ensureCustomBinaryPushed 安装自定义二进制应用前，把服务端二进制推送到节点。
func (a *API) ensureCustomBinaryPushed(ctx context.Context, w io.Writer, nodeID int64, appID string) error {
	if !strings.HasPrefix(appID, "custom-") {
		return nil
	}
	raw := strings.TrimPrefix(appID, "custom-")
	cid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	ca, err := a.store.GetCustomApp(cid)
	if err != nil {
		return nil
	}
	if ca.Type != "binary" {
		return nil
	}
	return a.apps.PushCustomBinary(ctx, w, nodeID, ca)
}

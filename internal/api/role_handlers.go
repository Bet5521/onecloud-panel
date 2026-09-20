package api

import (
	"net/http"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/store"
)

type roleDTO struct {
	ID          int64    `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Builtin     bool     `json:"builtin"`
	Protected   bool     `json:"protected"`
	Permissions []string `json:"permissions"`
	CreatedAt   int64    `json:"created_at"`
}

// GET /api/roles
func (a *API) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := a.store.ListRoles()
	if err != nil {
		writeError(w, 500, "角色查询失败")
		return
	}
	out := make([]roleDTO, 0, len(roles))
	for _, rl := range roles {
		perms, err := a.store.GetRolePermissions(rl.ID)
		if err != nil {
			writeError(w, 500, "权限查询失败")
			return
		}
		if perms == nil {
			perms = []string{}
		}
		out = append(out, roleDTO{
			ID: rl.ID, Code: rl.Code, Name: rl.Name,
			Builtin: rl.Builtin, Protected: rl.Protected,
			Permissions: perms, CreatedAt: rl.CreatedAt,
		})
	}
	writeJSON(w, map[string]any{
		"items":           out,
		"all_permissions": store.AllPermissions,
	})
}

type updateRoleReq struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

// PUT /api/roles/{id}
func (a *API) updateRole(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	rl, err := a.store.RoleByID(id)
	if err != nil {
		writeError(w, 404, "角色不存在")
		return
	}
	var req updateRoleReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	if rl.Protected {
		writeError(w, 400, "内置受保护角色不可修改")
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		if len(req.Name) > 32 {
			writeError(w, 400, "角色名最长 32 字")
			return
		}
	}
	// 权限点白名单校验
	valid := map[string]bool{}
	for _, p := range store.AllPermissions {
		valid[p] = true
	}
	seen := map[string]bool{}
	for _, p := range req.Permissions {
		if !valid[p] {
			writeError(w, 400, "未知权限点: "+p)
			return
		}
		if seen[p] {
			writeError(w, 400, "权限点重复: "+p)
			return
		}
		seen[p] = true
	}

	if strings.TrimSpace(req.Name) != "" {
		if err := a.store.UpdateRoleName(id, strings.TrimSpace(req.Name)); err != nil {
			writeError(w, 500, "角色名更新失败")
			return
		}
	}
	if req.Permissions != nil {
		if err := a.store.ReplaceRolePermissions(id, req.Permissions); err != nil {
			writeError(w, 500, "权限更新失败")
			return
		}
	}
	a.audit.Record(r, "role", "update", "role", strconv.FormatInt(id, 10),
		audit.ResultSuccess, audit.DetailJSON(map[string]any{
			"name": req.Name, "permissions": req.Permissions,
		}))

	updated, _ := a.store.RoleByID(id)
	perms, _ := a.store.GetRolePermissions(id)
	if perms == nil {
		perms = []string{}
	}
	writeJSON(w, roleDTO{
		ID: updated.ID, Code: updated.Code, Name: updated.Name,
		Builtin: updated.Builtin, Protected: updated.Protected,
		Permissions: perms, CreatedAt: updated.CreatedAt,
	})
}

package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/store"
)

// idFromPath 读取 {id} 路径参数。
func idFromPath(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// callerInfo 返回当前登录用户 ID 与管理员标识。
func callerInfo(ident *auth.Identity) (int64, bool) {
	if ident == nil || ident.User == nil {
		return 0, false
	}
	return ident.User.ID, ident.RoleCode == "admin"
}

// callerIsAdmin 当前请求用户是否为管理员。
func callerIsAdmin(r *http.Request) bool {
	_, admin := callerInfo(auth.FromContext(r.Context()))
	return admin
}

// callerID 返回当前登录用户 ID（未登录返回 0, false）。
func callerID(r *http.Request) (int64, bool) {
	ident := auth.FromContext(r.Context())
	if ident == nil || ident.User == nil {
		return 0, false
	}
	return ident.User.ID, true
}

// assertNodeOwner 节点属主断言（防 BOLA）：非管理员仅可操作自己添加的节点，
// 系统级节点（OwnerUserID 为空）仅管理员可访问。断言失败时返回 404 掩蔽节点存在性。
// 返回 false 表示已写出错误响应，调用方应立即返回。
func assertNodeOwner(w http.ResponseWriter, r *http.Request, n *store.Node) bool {
	if callerIsAdmin(r) {
		return true
	}
	uid, _ := callerID(r)
	if n.OwnerUserID == nil || *n.OwnerUserID != uid {
		writeError(w, http.StatusNotFound, "节点不存在")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

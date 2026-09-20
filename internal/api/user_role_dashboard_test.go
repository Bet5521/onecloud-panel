package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"onecloud-panel/internal/store"
)

func findUserID(t *testing.T, h http.Handler, jar http.CookieJar, username string) int64 {
	t.Helper()
	w := do(t, h, "GET", "/api/users", nil, jar)
	if w.Code != 200 {
		t.Fatalf("list users: %d", w.Code)
	}
	var resp struct {
		Items []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			RoleCode string `json:"role_code"`
		} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	for _, u := range resp.Items {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("用户 %s 未找到", username)
	return 0
}

func TestUserManagementAPI(t *testing.T) {
	_, s, h := newTestServer(t)
	adminJar := adminLogin(t, h)

	// 初始用户列表
	w := do(t, h, "GET", "/api/users", nil, adminJar)
	if w.Code != 200 || !containsAll(w.Body.String(), "rootadmin") {
		t.Fatalf("list users: %d %s", w.Code, w.Body.String())
	}

	// 角色 id
	roles, _ := s.ListRoles()
	var viewerRID, operatorRID int64
	for _, r := range roles {
		switch r.Code {
		case "viewer":
			viewerRID = r.ID
		case "operator":
			operatorRID = r.ID
		}
	}

	// 创建用户
	w = do(t, h, "POST", "/api/users", map[string]any{
		"username": "alice", "password": "AlicePass1", "role_id": viewerRID,
	}, adminJar)
	if w.Code != 200 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	// 重复
	w = do(t, h, "POST", "/api/users", map[string]any{
		"username": "alice", "password": "AlicePass1", "role_id": viewerRID,
	}, adminJar)
	if w.Code != 409 {
		t.Fatalf("dup user: %d", w.Code)
	}
	// 弱密码 / 坏角色
	if w = do(t, h, "POST", "/api/users", map[string]any{
		"username": "bob", "password": "short", "role_id": viewerRID,
	}, adminJar); w.Code != 400 {
		t.Fatalf("weak pwd: %d", w.Code)
	}
	if w = do(t, h, "POST", "/api/users", map[string]any{
		"username": "bob", "password": "BobPass12", "role_id": 9999,
	}, adminJar); w.Code != 400 {
		t.Fatalf("bad role: %d", w.Code)
	}
	aliceID := findUserID(t, h, adminJar, "alice")

	// alice 登录
	aliceJar := newJar(t)
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "alice", "password": "AlicePass1"}, aliceJar)
	if w.Code != 200 {
		t.Fatalf("alice login: %d", w.Code)
	}
	// viewer 无权用户管理
	if w = do(t, h, "GET", "/api/users", nil, aliceJar); w.Code != 403 {
		t.Fatalf("viewer list users: %d", w.Code)
	}

	// alice 改密：原密码错 →400
	if w = do(t, h, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "WrongPass1", "new_password": "AliceNew12",
	}, aliceJar); w.Code != 400 {
		t.Fatalf("wrong old: %d", w.Code)
	}
	// 正确改密
	if w = do(t, h, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "AlicePass1", "new_password": "AliceNew12",
	}, aliceJar); w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	// 新密码登录
	if w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "alice", "password": "AliceNew12"}, newJar(t)); w.Code != 200 {
		t.Fatalf("new password login: %d", w.Code)
	}

	// 管理员重置密码
	if w = do(t, h, "POST", "/api/users/"+itoa(aliceID)+"/password",
		map[string]string{"password": "AdminReset1"}, adminJar); w.Code != 200 {
		t.Fatalf("admin reset: %d %s", w.Code, w.Body.String())
	}

	// 改角色为 operator
	if w = do(t, h, "PUT", "/api/users/"+itoa(aliceID),
		map[string]any{"role_id": operatorRID}, adminJar); w.Code != 200 {
		t.Fatalf("change role: %d %s", w.Code, w.Body.String())
	}
	if !containsAll(do(t, h, "GET", "/api/users", nil, adminJar).Body.String(), `"role_code":"operator"`) {
		t.Fatal("角色未更新")
	}

	// 禁用 alice → 其登录失败
	if w = do(t, h, "PUT", "/api/users/"+itoa(aliceID),
		map[string]string{"status": "disabled"}, adminJar); w.Code != 200 {
		t.Fatalf("disable: %d", w.Code)
	}
	if w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "alice", "password": "AdminReset1"}, newJar(t)); w.Code == 200 {
		t.Fatal("禁用用户不应能登录")
	}

	// 不能禁用自己 / 删除自己
	rootID := findUserID(t, h, adminJar, "rootadmin")
	if w = do(t, h, "PUT", "/api/users/"+itoa(rootID),
		map[string]string{"status": "disabled"}, adminJar); w.Code != 400 {
		t.Fatalf("self disable: %d", w.Code)
	}
	if w = do(t, h, "DELETE", "/api/users/"+itoa(rootID), nil, adminJar); w.Code != 400 {
		t.Fatalf("self delete: %d", w.Code)
	}

	// 删除 alice
	if w = do(t, h, "DELETE", "/api/users/"+itoa(aliceID), nil, adminJar); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.UserByID(aliceID); err == nil {
		t.Fatal("删除后用户仍存在")
	}
}

func TestRolePermissionsAPI(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	// 角色列表含权限与全部权限点
	w := do(t, h, "GET", "/api/roles", nil, jar)
	if w.Code != 200 || !containsAll(w.Body.String(),
		"\"code\":\"admin\"", "\"code\":\"operator\"", "all_permissions") {
		t.Fatalf("list roles: %d %s", w.Code, w.Body.String())
	}

	// admin 受保护不可改
	var adminID, operatorID int64
	w = do(t, h, "GET", "/api/roles", nil, jar)
	var rr struct {
		Items []struct {
			ID        int64  `json:"id"`
			Code      string `json:"code"`
			Protected bool   `json:"protected"`
		} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rr)
	for _, r := range rr.Items {
		switch r.Code {
		case "admin":
			adminID = r.ID
		case "operator":
			operatorID = r.ID
		}
	}
	if w = do(t, h, "PUT", "/api/roles/"+itoa(adminID),
		map[string]any{"permissions": []string{"dashboard:read"}}, jar); w.Code != 400 {
		t.Fatalf("protected role: %d", w.Code)
	}

	// operator 调整权限（改名 + 精简权限）
	if w = do(t, h, "PUT", "/api/roles/"+itoa(operatorID), map[string]any{
		"name":        "运维员",
		"permissions": []string{"dashboard:read", "node:read"},
	}, jar); w.Code != 200 {
		t.Fatalf("update role: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, "GET", "/api/roles", nil, jar)
	var after struct {
		Items []struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
		} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &after)
	found := false
	for _, it := range after.Items {
		if it.Name == "运维员" {
			found = true
			if len(it.Permissions) != 2 || it.Permissions[0] != "dashboard:read" ||
				it.Permissions[1] != "node:read" {
				t.Fatalf("权限未按请求更新: %+v", it.Permissions)
			}
		}
	}
	if !found {
		t.Fatal("角色改名未生效")
	}

	// 非法权限点
	if w = do(t, h, "PUT", "/api/roles/"+itoa(operatorID),
		map[string]any{"permissions": []string{"hacker:all"}}, jar); w.Code != 400 {
		t.Fatalf("invalid perm: %d", w.Code)
	}
}

func TestDashboardAPI(t *testing.T) {
	_, s, h := appTestSetup(t)
	// viewer 也能看仪表盘
	jar := makeViewer(t, h, s, "dashviewer")
	w := do(t, h, "GET", "/api/dashboard/summary", nil, jar)
	if w.Code != 200 {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Stats map[string]int `json:"stats"`
		Nodes []struct {
			Mode string `json:"mode"`
		} `json:"nodes"`
		RecentAudits []store.AuditLog `json:"recent_audits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Stats["nodes_total"] != 1 || resp.Stats["nodes_online"] != 1 {
		t.Fatalf("本机节点统计错误: %+v", resp.Stats)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Mode != "local" {
		t.Fatalf("本机节点缺失: %+v", resp.Nodes)
	}
}

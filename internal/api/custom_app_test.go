package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCustomAppCRUD(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := newJar(t)

	// 初始化管理员
	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "admin", "password": "AdminPass123"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	// 1. 创建 Docker 自定义应用
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":         "my-nginx",
			"name":           "My Nginx",
			"category":       "Web",
			"icon":           "globe",
			"description":    "自定义 Nginx",
			"method":         "docker",
			"docker_image":   "nginx:latest",
			"docker_ports":   []string{"8080:80"},
			"docker_volumes": []string{"/data:/usr/share/nginx/html"},
			"docker_restart": "unless-stopped",
		}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create docker app: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID    int64  `json:"id"`
		AppID string `json:"app_id"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.AppID != "my-nginx" || created.Name != "My Nginx" {
		t.Fatalf("unexpected created app: %+v", created)
	}

	// 2. 创建 Native 自定义应用
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "my-tool",
			"name":         "My Tool",
			"method":       "native",
			"download_url": "https://github.com/user/repo/releases/latest/download/tool-linux-amd64",
			"unit_name":    "mytool.service",
		}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create native app: %d %s", w.Code, w.Body.String())
	}

	// 3. 列出自定义应用
	w = do(t, h, "GET", "/api/custom-apps", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("list custom apps: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Items []struct {
			AppID string `json:"app_id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("expected 2 custom apps, got %d", list.Total)
	}

	// 4. 验证配方列表包含自定义应用
	w = do(t, h, "GET", "/api/recipes", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("list recipes: %d %s", w.Code, w.Body.String())
	}
	var recipes struct {
		Items []struct {
			ID string `json:"ID"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recipes); err != nil {
		t.Fatalf("unmarshal recipes: %v", err)
	}
	// 2 built-in (demo, dtest) + 2 custom = 4
	if recipes.Total != 4 {
		t.Fatalf("expected 4 recipes, got %d", recipes.Total)
	}
	foundMyNginx := false
	foundMyTool := false
	for _, r := range recipes.Items {
		if r.ID == "my-nginx" {
			foundMyNginx = true
		}
		if r.ID == "my-tool" {
			foundMyTool = true
		}
	}
	if !foundMyNginx || !foundMyTool {
		t.Fatalf("custom apps not found in recipe list: my-nginx=%v, my-tool=%v", foundMyNginx, foundMyTool)
	}

	// 5. 查看配方详情
	w = do(t, h, "GET", "/api/recipes/my-nginx", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("get recipe: %d %s", w.Code, w.Body.String())
	}
	var recipe struct {
		ID     string `json:"ID"`
		Name   string `json:"Name"`
		Docker struct {
			Image string   `json:"Image"`
			Ports []string `json:"Ports"`
		} `json:"Docker"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recipe); err != nil {
		t.Fatalf("unmarshal recipe: %v", err)
	}
	if recipe.ID != "my-nginx" || recipe.Docker.Image != "nginx:latest" {
		t.Fatalf("unexpected recipe: %+v", recipe)
	}

	// 6. 更新自定义应用
	w = do(t, h, "PUT", "/api/custom-apps/1",
		map[string]any{
			"app_id":       "my-nginx",
			"name":         "My Nginx Updated",
			"method":       "docker",
			"docker_image": "nginx:1.25",
			"docker_ports": []string{"8080:80", "8443:443"},
			"docker_restart": "always",
		}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("update app: %d %s", w.Code, w.Body.String())
	}

	// 7. 验证更新
	w = do(t, h, "GET", "/api/recipes/my-nginx", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("get updated recipe: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recipe); err != nil {
		t.Fatalf("unmarshal updated recipe: %v", err)
	}
	if recipe.Name != "My Nginx Updated" || recipe.Docker.Image != "nginx:1.25" {
		t.Fatalf("update not applied: %+v", recipe)
	}

	// 8. 删除自定义应用
	w = do(t, h, "DELETE", "/api/custom-apps/1", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("delete app: %d %s", w.Code, w.Body.String())
	}

	// 9. 验证删除
	w = do(t, h, "GET", "/api/custom-apps", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("list after delete: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list after delete: %v", err)
	}
	if list.Total != 1 {
		t.Fatalf("expected 1 custom app after delete, got %d", list.Total)
	}

	// 10. 验证配方列表不再包含已删除的自定义应用
	w = do(t, h, "GET", "/api/recipes", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("list recipes after delete: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recipes); err != nil {
		t.Fatalf("unmarshal recipes after delete: %v", err)
	}
	if recipes.Total != 3 { // 2 built-in + 1 custom
		t.Fatalf("expected 3 recipes after delete, got %d", recipes.Total)
	}
	for _, r := range recipes.Items {
		if r.ID == "my-nginx" {
			t.Fatal("deleted custom app still in recipe list")
		}
	}
}

func TestCustomAppValidation(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := newJar(t)

	// 初始化管理员
	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "admin", "password": "AdminPass123"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	// 1. 无效 app_id
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "Invalid_ID!",
			"name":         "Test",
			"method":       "docker",
			"docker_image": "nginx:latest",
		}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid app_id, got %d", w.Code)
	}

	// 2. 空名称
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "test-app",
			"name":         "",
			"method":       "docker",
			"docker_image": "nginx:latest",
		}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d", w.Code)
	}

	// 3. Docker 方式缺少镜像
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id": "test-app",
			"name":   "Test",
			"method": "docker",
		}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing docker image, got %d", w.Code)
	}

	// 4. Native 方式缺少下载地址
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":    "test-app",
			"name":      "Test",
			"method":    "native",
			"unit_name": "test.service",
		}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing download url, got %d", w.Code)
	}

	// 5. Native 方式缺少服务名
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "test-app",
			"name":         "Test",
			"method":       "native",
			"download_url": "https://example.com/app",
		}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing unit name, got %d", w.Code)
	}

	// 6. 与内置配方冲突
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "demo",
			"name":         "Demo",
			"method":       "docker",
			"docker_image": "nginx:latest",
		}, jar)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for builtin conflict, got %d", w.Code)
	}

	// 7. 重复 app_id
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "test-app",
			"name":         "Test",
			"method":       "docker",
			"docker_image": "nginx:latest",
		}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("first create: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "test-app",
			"name":         "Test 2",
			"method":       "docker",
			"docker_image": "nginx:latest",
		}, jar)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate app_id, got %d", w.Code)
	}
}

func TestCustomAppPermission(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := newJar(t)

	// 初始化管理员
	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "admin", "password": "AdminPass123"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	// 创建只读用户
	w = do(t, h, "POST", "/api/users",
		map[string]any{
			"username": "viewer",
			"password": "ViewerPass123",
			"role_id":  3, // viewer
		}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create viewer: %d %s", w.Code, w.Body.String())
	}

	// 用只读用户登录
	viewerJar := newJar(t)
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "viewer", "password": "ViewerPass123"}, viewerJar)
	if w.Code != http.StatusOK {
		t.Fatalf("login viewer: %d %s", w.Code, w.Body.String())
	}

	// 只读用户可以查看
	w = do(t, h, "GET", "/api/custom-apps", nil, viewerJar)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer list: %d %s", w.Code, w.Body.String())
	}

	// 只读用户不能创建
	w = do(t, h, "POST", "/api/custom-apps",
		map[string]any{
			"app_id":       "test-app",
			"name":         "Test",
			"method":       "docker",
			"docker_image": "nginx:latest",
		}, viewerJar)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer create, got %d", w.Code)
	}
}
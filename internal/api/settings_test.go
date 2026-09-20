package api

import "testing"

func TestSettingsValidation(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)

	// 非法 github_proxy
	for _, v := range []string{
		"ftp://gh-proxy.com",
		"not a url",
		"https://",
		"://no-scheme",
	} {
		if w := do(t, h, "PUT", "/api/settings",
			map[string]string{"github_proxy": v}, jar); w.Code != 400 {
			t.Errorf("github_proxy=%q 应 400，实际 %d: %s", v, w.Code, w.Body.String())
		}
	}

	// 合法 URL 与清空（直连）
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"github_proxy": "https://gh-proxy.com"}, jar); w.Code != 200 {
		t.Fatalf("合法 github_proxy 应 200: %d %s", w.Code, w.Body.String())
	}
	got, _, _ := s.GetSetting("github_proxy")
	if got != "https://gh-proxy.com" {
		t.Fatalf("github_proxy 持久化失败: %q", got)
	}
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"github_proxy": ""}, jar); w.Code != 200 {
		t.Fatalf("清空 github_proxy 应 200: %d", w.Code)
	}

	// 不可编辑项
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"admin_password": "x"}, jar); w.Code != 400 {
		t.Fatalf("非可编辑项应 400，实际 %d", w.Code)
	}

	// 面板名称
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"panel_name": ""}, jar); w.Code != 400 {
		t.Fatalf("空 panel_name 应 400，实际 %d", w.Code)
	}
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"panel_name": "测试集群"}, jar); w.Code != 200 {
		t.Fatalf("合法 panel_name 应 200: %d", w.Code)
	}

	// 保留天数
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"audit_retention_days": "abc"}, jar); w.Code != 400 {
		t.Fatalf("非法保留天数应 400，实际 %d", w.Code)
	}
	if w := do(t, h, "PUT", "/api/settings",
		map[string]string{"audit_retention_days": "30"}, jar); w.Code != 200 {
		t.Fatalf("合法保留天数应 200: %d", w.Code)
	}
}

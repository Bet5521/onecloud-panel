package agent

import "testing"

func TestEngineAllowed(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/_ping", true},
		{"GET", "/version", true},
		{"GET", "/containers/json", true},
		{"GET", "/containers/abc123/json", true},
		{"GET", "/containers/abc123/logs?stdout=1", true},
		{"POST", "/containers/create", true},
		{"POST", "/containers/abc/start", true},
		{"DELETE", "/containers/abc", true},
		// 拒绝：高危端点、未知路径/方法
		{"GET", "/containers/abc/exec", false},
		{"POST", "/build", false},
		{"POST", "/containers/abc/exec", false},
		{"GET", "/secrets", false},
		{"PATCH", "/containers/abc", false},
		{"GET", "/..%2fetc/passwd", false},
	}
	for _, c := range cases {
		if got := engineAllowed(c.method, c.path); got != c.want {
			t.Errorf("engineAllowed(%s %s)=%v want %v", c.method, c.path, got, c.want)
		}
	}
}

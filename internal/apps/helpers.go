package apps

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"onecloud-panel/internal/store"
)

// decodeJSONReader 解码 HTTP 响应 JSON。
func decodeJSONReader(resp *http.Response, v any) error {
	return json.NewDecoder(resp.Body).Decode(v)
}

func osMode(m uint32, def os.FileMode) os.FileMode {
	if m == 0 {
		return def
	}
	return os.FileMode(m)
}

func paramsJSON(vars map[string]string) string {
	b, err := json.Marshal(vars)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// installationVars 从安装记录 Params 回填安装时变量（状态/动作/卸载路径使用）。
func installationVars(in *store.AppInstallation) map[string]string {
	vars := map[string]string{}
	if in != nil && in.Params != "" {
		_ = json.Unmarshal([]byte(in.Params), &vars)
	}
	return vars
}

func indentLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// tail 返回输出末尾最多 n 字节（错误信息用）。
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

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

// installParams 安装记录 Params 的持久化结构。无 Docker 覆盖时保持旧的扁平
// vars JSON，便于旧数据/旧版本兼容。
type installParams struct {
	Vars   map[string]string `json:"vars,omitempty"`
	Docker *DockerOverride   `json:"docker,omitempty"`
}

// paramsJSON 序列化安装参数（vars + 可选 Docker 覆盖）。
func paramsJSON(vars map[string]string, ov *DockerOverride) string {
	var b []byte
	var err error
	if ov == nil {
		b, err = json.Marshal(vars)
	} else {
		b, err = json.Marshal(installParams{Vars: vars, Docker: ov})
	}
	if err != nil {
		return "{}"
	}
	return string(b)
}

// installationVars 从安装记录 Params 回填安装时变量（状态/动作/卸载路径使用），
// 兼容旧扁平 JSON 与新 {"vars":{...}} 结构。
func installationVars(in *store.AppInstallation) map[string]string {
	vars := map[string]string{}
	if in == nil || in.Params == "" {
		return vars
	}
	var np installParams
	if err := json.Unmarshal([]byte(in.Params), &np); err == nil && np.Vars != nil {
		return np.Vars
	}
	_ = json.Unmarshal([]byte(in.Params), &vars)
	return vars
}

// installationDocker 从安装记录 Params 回填 Docker 覆盖（旧记录返回 nil）。
func installationDocker(in *store.AppInstallation) *DockerOverride {
	if in == nil || in.Params == "" {
		return nil
	}
	var np installParams
	if err := json.Unmarshal([]byte(in.Params), &np); err == nil {
		return np.Docker
	}
	return nil
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

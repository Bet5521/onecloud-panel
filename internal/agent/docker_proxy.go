package agent

import (
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	"onecloud-panel/internal/docker"
)

// dockerSocketPath 允许通过环境变量覆盖（测试用）。
const dockerSocketPath = "/var/run/docker.sock"

// 允许的 Engine API 路径模式。仅暴露面板管理容器所需的端点，
// 拒绝 exec/build/attach/commit/cp/secrets/swarm 等高风险能力。
var allowedDocker = []struct {
	method string
	re     *regexp.Regexp
}{
	{http.MethodGet, regexp.MustCompile(`^/_ping$`)},
	{http.MethodGet, regexp.MustCompile(`^/version$`)},
	{http.MethodGet, regexp.MustCompile(`^/info$`)},
	{http.MethodGet, regexp.MustCompile(`^/containers/json$`)},
	{http.MethodGet, regexp.MustCompile(`^/containers/[^/]+/json$`)},
	{http.MethodGet, regexp.MustCompile(`^/containers/[^/]+/logs$`)},
	{http.MethodGet, regexp.MustCompile(`^/containers/[^/]+/stats$`)},
	{http.MethodGet, regexp.MustCompile(`^/images/json$`)},
	{http.MethodGet, regexp.MustCompile(`^/images/[^/]+/json$`)},
	{http.MethodGet, regexp.MustCompile(`^/networks$`)},
	{http.MethodGet, regexp.MustCompile(`^/volumes$`)},
	{http.MethodPost, regexp.MustCompile(`^/containers/create$`)},
	{http.MethodPost, regexp.MustCompile(`^/containers/[^/]+/start$`)},
	{http.MethodPost, regexp.MustCompile(`^/containers/[^/]+/stop$`)},
	{http.MethodPost, regexp.MustCompile(`^/containers/[^/]+/restart$`)},
	{http.MethodPost, regexp.MustCompile(`^/containers/[^/]+/kill$`)},
	{http.MethodPost, regexp.MustCompile(`^/containers/prune$`)},
	{http.MethodPost, regexp.MustCompile(`^/images/create$`)},
	{http.MethodDelete, regexp.MustCompile(`^/containers/[^/]+$`)},
	{http.MethodDelete, regexp.MustCompile(`^/images/[^/]+$`)},
}

// engineAllowed 判断 Engine API 方法+路径是否在白名单内。
// path 可能带查询串（如 /containers/abc/logs?stdout=1），仅按路径部分匹配。
func engineAllowed(method, p string) bool {
	// 拒绝路径穿越
	if strings.Contains(p, "..") {
		return false
	}
	p = strings.SplitN(p, "?", 2)[0]
	p = path.Clean(p)
	for _, rule := range allowedDocker {
		if rule.method == method && rule.re.MatchString(p) {
			return true
		}
	}
	return false
}

// dockerProxy 把面板侧请求透传到本机 Docker Engine unix socket。
// 仅允许 engineAllowed 白名单内的端点，防止面板被利用接管 Docker。
func (s *server) dockerProxy(w http.ResponseWriter, r *http.Request) {
	enginePath := strings.TrimPrefix(r.URL.Path, "/v1/docker")
	if enginePath == "" {
		enginePath = "/"
	}

	if !engineAllowed(r.Method, enginePath) {
		agentError(w, http.StatusForbidden, "Docker 端点不在白名单内")
		return
	}

	// 透传到本机 socket；不设整体超时，由 ctx 控制（logs/stats 可能是流式）。
	client := docker.NewLocalClient(dockerSocketPath, 0)
	target := "http://docker" + enginePath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		agentError(w, http.StatusInternalServerError, "构造请求失败")
		return
	}
	// 透传内容类型
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	resp, err := client.Do(req)
	if err != nil {
		agentError(w, http.StatusBadGateway, "Docker 不可达: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 透传响应头与状态码
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

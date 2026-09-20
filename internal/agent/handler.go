package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/system"
)

type server struct {
	exec executor.Executor
	tok  *tokenHolder
}

func newServer(tok *tokenHolder) *server {
	return &server{exec: executor.NewLocal(nil), tok: tok}
}

func (s *server) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 以下全部需要 Token
	protected := http.NewServeMux()
	protected.HandleFunc("GET /v1/info", s.info)
	protected.HandleFunc("POST /v1/exec", s.execCmd)
	protected.HandleFunc("POST /v1/exec-stream", s.execStream)
	protected.HandleFunc("POST /v1/systemctl", s.systemctl)
	protected.HandleFunc("GET /v1/journal", s.journal)
	protected.HandleFunc("GET /v1/file", s.readFile)
	protected.HandleFunc("PUT /v1/file", s.writeFile)
	protected.HandleFunc("POST /v1/download", s.download)
	protected.HandleFunc("POST /v1/healthcheck", s.healthcheck)
	protected.HandleFunc("POST /v1/token/rotate", s.rotate)
	// Docker Engine API 隧道（方法/路径白名单）
	protected.HandleFunc("/v1/docker/", s.dockerProxy)
	mux.Handle("/", s.auth(protected))
	return mux
}

func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		h := r.Header.Get(authHeader)
		if !strings.HasPrefix(h, bearer) {
			agentError(w, http.StatusUnauthorized, "缺少 Token")
			return
		}
		token := strings.TrimPrefix(h, bearer)
		cur := s.tok.get()
		if cur == "" || subtle.ConstantTimeCompare([]byte(token), []byte(cur)) != 1 {
			agentError(w, http.StatusUnauthorized, "Token 无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) info(w http.ResponseWriter, r *http.Request) {
	host, err := system.Collect()
	if err != nil {
		agentError(w, http.StatusInternalServerError, "采集失败: "+err.Error())
		return
	}
	writeAgentJSON(w, host)
}

func (s *server) execCmd(w http.ResponseWriter, r *http.Request) {
	var req ExecReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		agentError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 30*time.Minute {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	res, err := s.exec.Exec(ctx, req.Command, req.Args...)
	if err != nil {
		writeAgentJSON(w, &ExecResp{ExitCode: -1, Output: err.Error()})
		return
	}
	writeAgentJSON(w, &ExecResp{ExitCode: res.ExitCode, Output: res.Output})
}

func (s *server) execStream(w http.ResponseWriter, r *http.Request) {
	var req ExecReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		agentError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	fw := &flushWriter{w: w, f: flusher}
	code, err := s.exec.ExecStream(r.Context(), fw, req.Command, req.Args...)
	if err != nil {
		_, _ = w.Write([]byte("\n[exit " + strconv.Itoa(code) + ": " + err.Error() + "]"))
	}
}

var allowedSystemctl = map[string]bool{
	"start": true, "stop": true, "restart": true,
	"enable": true, "disable": true,
	"status": true, "is-active": true, "is-enabled": true, "daemon-reload": true,
}

func (s *server) systemctl(w http.ResponseWriter, r *http.Request) {
	var req SystemctlReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		agentError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !allowedSystemctl[req.Action] || req.Unit == "" {
		agentError(w, http.StatusBadRequest, "非法 systemctl 操作")
		return
	}
	res, err := s.exec.Exec(r.Context(), "systemctl", req.Action, req.Unit)
	if err != nil {
		agentError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentJSON(w, &ExecResp{ExitCode: res.ExitCode, Output: res.Output})
}

func (s *server) journal(w http.ResponseWriter, r *http.Request) {
	unit := r.URL.Query().Get("unit")
	if unit == "" {
		agentError(w, http.StatusBadRequest, "缺少 unit")
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			lines = n
		}
	}
	follow := r.URL.Query().Get("follow") == "1"

	args := []string{"--no-pager", "-u", unit, "-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		flusher, _ := w.(http.Flusher)
		code, err := s.exec.ExecStream(r.Context(), &flushWriter{w: w, f: flusher}, "journalctl", args...)
		if err != nil && code != 0 {
			return
		}
		return
	}
	res, err := s.exec.Exec(r.Context(), "journalctl", args...)
	if err != nil {
		agentError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(res.Output))
}

func (s *server) readFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		agentError(w, http.StatusBadRequest, "缺少 path")
		return
	}
	b, err := s.exec.ReadFile(path)
	if err != nil {
		agentError(w, http.StatusNotFound, err.Error())
		return
	}
	writeAgentJSON(w, &FileResp{Path: path, Content: string(b)})
}

func (s *server) writeFile(w http.ResponseWriter, r *http.Request) {
	var req FileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		agentError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := s.exec.WriteFile(req.Path, []byte(req.Content)); err != nil {
		agentError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentJSON(w, map[string]string{"status": "ok"})
}

func (s *server) rotate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Token) < 16 {
		agentError(w, http.StatusBadRequest, "新 Token 非法")
		return
	}
	s.tok.set(req.Token)
	writeAgentJSON(w, map[string]string{"status": "ok"})
}

func (s *server) download(w http.ResponseWriter, r *http.Request) {
	var req DownloadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" || req.Dest == "" {
		agentError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	mode := os.FileMode(req.Mode)
	if mode == 0 {
		mode = 0o755
	}
	dl, ok := s.exec.(executor.Downloader)
	if !ok {
		agentError(w, http.StatusInternalServerError, "执行器不支持下载")
		return
	}
	if err := dl.Download(r.Context(), req.URL, req.Dest, mode, req.SHA256, nil); err != nil {
		writeAgentJSON(w, &DownloadResp{OK: false, Error: err.Error()})
		return
	}
	writeAgentJSON(w, &DownloadResp{OK: true})
}

func (s *server) healthcheck(w http.ResponseWriter, r *http.Request) {
	var req HealthcheckReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		agentError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	hc, ok := s.exec.(executor.HealthChecker)
	if !ok {
		agentError(w, http.StatusInternalServerError, "执行器不支持健康检查")
		return
	}
	ok2, detail := hc.Healthcheck(r.Context(), req.Type, req.Port, req.Path)
	writeAgentJSON(w, map[string]any{"ok": ok2, "detail": detail})
}

type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

func writeAgentJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func agentError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

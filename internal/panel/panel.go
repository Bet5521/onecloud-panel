package panel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"onecloud-panel/internal/api"
	"onecloud-panel/internal/apps"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/config"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/self"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
	"onecloud-panel/internal/tlssniff"
	"onecloud-panel/internal/version"
	"onecloud-panel/internal/web"
)

// Run 启动面板服务。
func Run(cfg *config.Panel) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}

	// ---- 存储 ----
	s, err := store.Open(filepath.Join(cfg.DataDir, "panel.db"))
	if err != nil {
		return err
	}
	defer s.Close()

	// ---- 密钥盒 / 认证 / 审计 / 节点 / API ----
	box, err := secretbox.New(cfg.DataDir)
	if err != nil {
		return err
	}
	sessions := auth.NewManager(s, 0)
	asvc := audit.New(s)
	limiter := auth.NewLoginLimiter(5, 5*time.Minute)
	authH := auth.NewHandler(s, sessions, limiter, asvc.Login)
	nodeSvc := node.New(s, box)
	taskRunner := runner.New(s)

	// 加载内置配方（启动即校验，坏配方直接失败）
	recipeReg, err := recipes.LoadBuiltin()
	if err != nil {
		return err
	}

	// 应用管理器（直装/容器）并把生命周期挂到任务运行器
	appManager := apps.New(s, nodeSvc, recipeReg, box, asvc)
	appManager.RegisterRunnerTasks(taskRunner)

	// 自定义应用：注入二进制目录并向注册表补登合成配方
	appManager.SetBinaryDir(filepath.Join(cfg.DataDir, "custom-apps"))
	if err := appManager.RegisterCustomApps(); err != nil {
		log.Printf("注册自定义应用配方失败: %v", err)
	}

	apiObj := api.New(s, authH, auth.NewMiddleware(sessions), asvc,
		nodeSvc, recipeReg, taskRunner, appManager)

	// 应用生命周期事件 → 通知分发引擎（通道级 × 用户级两级订阅过滤）
	appManager.NotifyEvent = func(event, title, body string) {
		apiObj.EmitEvent(event, title, body)
	}

	// 发布目录：默认 <data-dir>/releases，放置各架构二进制供 install.sh 下载
	releaseDir := cfg.ReleaseDir
	if releaseDir == "" {
		releaseDir = filepath.Join(cfg.DataDir, "releases")
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return err
	}
	apiObj.SetReleaseDir(releaseDir)
	apiObj.SetDataDir(cfg.DataDir)
	apiObj.SetListenAddr(cfg.Listen)

	// ---- 面板自身管理（状态/日志/异步重启） ----
	selfSvc := self.New(s, executor.NewLocal(nil), cfg.DataDir, cfg.Listen, cfg.UnitName)
	if n, err := selfSvc.FinalizeBoot(); err != nil {
		log.Printf("重启任务收口失败: %v", err)
	} else if n > 0 {
		log.Printf("已确认 %d 次面板重启完成", n)
	}
	apiObj.SetSelfService(selfSvc)
	apiObj.SetSessionManager(sessions)
	apiObj.RegisterSSHTasks(taskRunner)
	apiHandler := apiObj.Handler()

	// ---- 生命周期上下文 ----
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ---- 后台任务运行器 ----
	go taskRunner.Start(ctx)

	// ---- 通知状态探测（节点/应用上线离线）与定时摘要循环 ----
	go apiObj.NotifyProbeLoop(ctx)
	go apiObj.NotifyScheduleLoop(ctx)

	// ---- 本机节点初始化与周期刷新 ----
	refreshLocalNode(nodeSvc)
	go localNodeLoop(ctx, nodeSvc)

	// ---- 前端 ----
	assets, err := fs.Sub(web.Assets, "assets")
	if err != nil {
		return err
	}
	fileServer := http.FileServer(http.FS(assets))
	// SPA 包装：index.html 始终重新校验（避免升级后缓存旧 index 引用旧 chunk），
	// 带内容哈希的 /static/ 资源可永久缓存。
	spaServer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ---- 从面板设置加载 TLS 状态（默认关闭；命令行 --tls-cert/--tls-key 优先） ----
	tlsCfg, forceHTTPS := loadTLSConfig(s, cfg)
	// 启用 HTTPS 时给会话 Cookie 加 Secure 属性（明文链路不再回传会话令牌）
	sessions.SetSecure(tlsCfg != nil || forceHTTPS)

	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 强制 HTTPS：明文 HTTP 请求 301 跳转到 HTTPS（健康检查除外）
		if forceHTTPS && r.TLS == nil && r.URL.Path != "/healthz" {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(),
				http.StatusMovedPermanently)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			r.URL.Path == "/install.sh" ||
			strings.HasPrefix(r.URL.Path, "/dl/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// SPA：未知路径回退 index.html
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(assets, p); errors.Is(err, fs.ErrNotExist) {
				r.URL.Path = "/"
			}
		}
		spaServer.ServeHTTP(w, r)
	})
	mux.Handle("/", rootHandler)

	// ---- 定期清理 ----
	go maintenanceLoop(ctx, s, asvc)

	srv := &http.Server{
		Handler:           api.SecurityChain(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // 允许大文件上传/慢速请求体，但不做无限等待
		WriteTimeout:      0,               // 日志流/任务流为长连接，由 ctx 控制
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	// 同一 TCP 端口嗅探 HTTP/HTTPS（tlsCfg 为 nil 时纯 HTTP）
	rawLn, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Listen, err)
	}
	ln := tlssniff.New(rawLn, tlsCfg)

	go func() {
		<-ctx.Done()
		log.Printf("收到关停信号，正在优雅关闭…")
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	scheme := "http"
	if tlsCfg != nil {
		scheme = "https+http"
	}
	log.Printf("%s 面板模式启动，监听 %s://%s，数据目录 %s%s",
		version.Print(), scheme, cfg.Listen, cfg.DataDir,
		forceSuffix(forceHTTPS))
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Println("面板已停止")
	return nil
}

func forceSuffix(force bool) string {
	if force {
		return "（已强制 HTTPS）"
	}
	return ""
}

// loadTLSConfig 解析 TLS 启用状态：
// 1) 命令行 --tls-cert 与 --tls-key 同时提供时优先（兼容旧部署）；
// 2) 否则读取面板设置 tls_enabled 与证书文件路径；
// 3) 默认返回 nil（纯 HTTP）。
func loadTLSConfig(s *store.Store, cfg *config.Panel) (*tls.Config, bool) {
	certFile, keyFile := cfg.TLSCert, cfg.TLSKey
	if certFile == "" || keyFile == "" {
		if v, _, _ := s.GetSetting("tls_enabled"); v == "1" {
			cf, _, _ := s.GetSetting("tls_cert_file")
			kf, _, _ := s.GetSetting("tls_key_file")
			certFile, keyFile = cf, kf
		}
	}
	force := false
	if v, _, _ := s.GetSetting("tls_force_https"); v == "1" {
		force = true
	}
	if certFile == "" || keyFile == "" {
		return nil, false
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Printf("警告: TLS 证书加载失败（%v），本次以纯 HTTP 启动", err)
		return nil, false
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, force
}

func refreshLocalNode(svc *node.Service) {
	host, err := system.Collect()
	if err != nil {
		host = nil // 非 Linux 开发环境
	}
	if _, err := svc.EnsureLocalNode(host); err != nil {
		log.Printf("本机节点初始化失败: %v", err)
	}
}

func localNodeLoop(ctx context.Context, svc *node.Service) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshLocalNode(svc)
		}
	}
}

func maintenanceLoop(ctx context.Context, s *store.Store, asvc *audit.Service) {
	// 启动后 1 分钟执行一次，之后每 24 小时
	run := func() {
		if v, _, err := s.GetSetting("audit_retention_days"); err == nil && v != "" {
			if days, err := strconv.Atoi(v); err == nil && days > 0 {
				if n, err := asvc.RetentionCleanup(days); err == nil && n > 0 {
					log.Printf("清理过期审计日志 %d 条", n)
				}
			}
		}
		if n, err := s.CleanExpiredSessions(); err == nil && n > 0 {
			log.Printf("清理过期会话 %d 条", n)
		}
	}
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			run()
			timer.Reset(24 * time.Hour)
		}
	}
}

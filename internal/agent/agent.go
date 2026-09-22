package agent

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"onecloud-panel/internal/config"
	"onecloud-panel/internal/system"
	"onecloud-panel/internal/version"
)

const heartbeatInterval = 30 * time.Second

// Run 启动节点 Agent。
func Run(cfg *config.Agent) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}

	state, err := loadState(cfg.DataDir)
	if err != nil {
		return err
	}
	if cfg.Server != "" {
		state.Server = strings.TrimRight(cfg.Server, "/")
	}
	applyConfigToken(state, cfg.Token)
	if state.Server == "" {
		return errors.New("缺少面板地址 --server")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 首次注册
	if state.Token == "" {
		if cfg.RegisterToken == "" {
			return errors.New("需要 --register-token（首次注册）或 --token（已注册）完成纳管")
		}
		port := listenPort(cfg.Listen)
		if err := registerLoop(ctx, state, cfg.RegisterToken, port, cfg.InsecureTLS); err != nil {
			return err
		}
		if err := saveState(cfg.DataDir, state); err != nil {
			return err
		}
		log.Printf("注册成功，节点 ID %d", state.NodeID)
	}

	holder := &tokenHolder{}
	holder.set(state.Token)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           newServer(holder).mux(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go heartbeatLoop(ctx, cfg.DataDir, state, holder, cfg.InsecureTLS)

	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	log.Printf("%s Agent 启动，监听 %s，面板 %s，节点 %d",
		version.Print(), cfg.Listen, state.Server, state.NodeID)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func registerLoop(ctx context.Context, state *State, registerToken string, port int, insecure bool) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		regCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		resp, err := Register(regCtx, state.Server, registerToken, port, insecure)
		cancel()
		if err == nil {
			state.Token = resp.Token
			state.NodeID = resp.NodeID
			return nil
		}
		log.Printf("注册失败（将重试）: %v", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func heartbeatLoop(ctx context.Context, dataDir string, state *State, holder *tokenHolder, insecure bool) {
	beat := func() {
		host, err := system.Collect()
		if err != nil {
			log.Printf("主机采集失败: %v", err)
			return
		}
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		resp, err := Heartbeat(hctx, state.Server, holder.get(), host, insecure)
		if err != nil {
			log.Printf("心跳失败: %v", err)
			return
		}
		if resp.RotateToken != "" && resp.RotateToken != holder.get() {
			holder.set(resp.RotateToken)
			state.Token = resp.RotateToken
			if err := saveState(dataDir, state); err != nil {
				log.Printf("Token 轮换落盘失败: %v", err)
			} else {
				log.Println("Agent Token 已轮换")
			}
		}
	}
	beat()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}

// applyConfigToken 本地无注册身份时用命令行/环境传入的长期 Token 兜底
// （agent.json 丢失后按 --token 重装的场景）；已有本地身份时保持不动，
// 避免用 env 里可能过期的值覆盖心跳轮换后的新 Token。
func applyConfigToken(state *State, token string) {
	if state.Token == "" && token != "" {
		state.Token = token
	}
}

func listenPort(listen string) int {
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(listen, "tcp://"))
	if err != nil {
		return 9000
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return 9000
	}
	return p
}

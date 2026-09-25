package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/sshx"
	"onecloud-panel/internal/store"
)

// 终端 WebSocket 帧协议（JSON，终端数据 base64）：
//
//	客户端→服务端：{action:"start", user, password, port, term, host_key_fingerprint}（首帧/指纹确认后重发）；
//	              {type:"input", data}；{type:"resize", cols, rows}
//	服务端→客户端：{type:"hostkey", fingerprint}（首连待确认）；{type:"started"}；
//	              {type:"output", data}；{type:"exit"}；{type:"error", message}
//
// 密码仅在内存中使用，不落库、不写日志；连接关闭即丢弃。

const (
	terminalWriteWait  = 10 * time.Second
	terminalDialWait   = 10 * time.Second
	terminalMaxFrame   = 128 << 10 // 输入帧上限（容纳粘贴内容）
	terminalOutBufSize = 8 << 10
)

// terminalFrame 通用帧（入站）；出站帧用匿名结构按需构造。
type terminalFrame struct {
	Action             string `json:"action,omitempty"`
	Type               string `json:"type,omitempty"`
	Data               string `json:"data,omitempty"`
	Cols               int    `json:"cols,omitempty"`
	Rows               int    `json:"rows,omitempty"`
	User               string `json:"user,omitempty"`
	Password           string `json:"password,omitempty"`
	Port               int    `json:"port,omitempty"`
	Term               string `json:"term,omitempty"`
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
}

// terminalOut 并发安全的 WS 写封装（gorilla 连接不支持并发写）。
type terminalOut struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (o *terminalOut) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.ws.SetWriteDeadline(time.Now().Add(terminalWriteWait))
	return o.ws.WriteMessage(websocket.TextMessage, b)
}

func (o *terminalOut) sendError(msg string) {
	_ = o.send(map[string]any{"type": "error", "message": msg})
}

// GET /api/nodes/{id}/terminal — 交互式 SSH 终端（WebSocket）。
func (a *API) nodeTerminal(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := a.store.GetNode(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	up := websocket.Upgrader{
		ReadBufferSize:  32 << 10,
		WriteBufferSize: 32 << 10,
		CheckOrigin: func(req *http.Request) bool {
			o := req.Header.Get("Origin")
			if o == "" {
				return true
			}
			u, err := url.Parse(o)
			return err == nil && u.Host == r.Host
		},
	}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade 已写响应
	}
	a.terminalSession(r, ws, n, strconv.FormatInt(n.ID, 10))
}

// terminalSession 承载一次终端连接的完整生命周期。
func (a *API) terminalSession(r *http.Request, ws *websocket.Conn, n *store.Node, nodeID string) {
	defer ws.Close()
	out := &terminalOut{ws: ws}

	host := terminalHostOf(n)
	var (
		start  *terminalFrame
		client *sshx.Client
		shell  *sshx.Shell
		opened bool
	)
	defer func() {
		if shell != nil {
			shell.Close()
		}
		if client != nil {
			client.Close()
		}
		if opened {
			a.audit.Record(r, "node", "terminal_close", "node", nodeID, audit.ResultSuccess,
				audit.DetailJSON(map[string]any{"host": host, "user": start.User}))
		}
	}()

	// 阶段一：等待 start 帧并建立 SSH（含指纹确认重试）。
	for attempt := 0; attempt < 5; attempt++ {
		f, err := readTerminalFrame(ws)
		if err != nil {
			return
		}
		if f.Action != "start" {
			continue
		}
		start = f
		if strings.TrimSpace(start.User) == "" {
			start.User = "root"
		}
		if start.Password == "" {
			out.sendError("请填写 SSH 密码")
			continue
		}
		if start.Port == 0 {
			start.Port = 22
		}
		cfg := sshx.DialConfig{
			Host:               host,
			User:               start.User,
			Password:           start.Password,
			Port:               start.Port,
			HostKeyPolicy:      sshx.PolicyPin,
			HostKeyFingerprint: start.HostKeyFingerprint,
			Timeout:            terminalDialWait,
		}
		if a.dataDir != "" {
			cfg.KnownHostsFile = filepath.Join(a.dataDir, "known_hosts")
		}
		c, err := sshx.Dial(cfg, nil)
		if err != nil {
			var hk *sshx.HostKeyError
			if errors.As(err, &hk) {
				// 首连：回报指纹，等用户确认后重发 start。
				_ = out.send(map[string]any{"type": "hostkey", "fingerprint": hk.Fingerprint})
				start = nil
				continue
			}
			out.sendError(err.Error())
			return
		}
		client = c
		break
	}
	if client == nil {
		return
	}

	// 阶段二：申请 PTY 并启动远端 Shell。
	shell, err := client.OpenShell(start.Term, 0, 0)
	if err != nil {
		out.sendError(err.Error())
		return
	}
	opened = true
	if err := out.send(map[string]any{"type": "started"}); err != nil {
		return
	}
	a.audit.Record(r, "node", "terminal_open", "node", nodeID, audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"host": host, "user": start.User}))

	// 输出泵：PTY → WS。
	go func() {
		buf := make([]byte, terminalOutBufSize)
		for {
			nr, rerr := shell.Stdout().Read(buf)
			if nr > 0 {
				if out.send(map[string]any{
					"type": "output",
					"data": base64.StdEncoding.EncodeToString(buf[:nr]),
				}) != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// 远端 Shell 退出：通知客户端并断开（促使下方读循环结束）。
	go func() {
		_ = shell.Wait()
		_ = out.send(map[string]any{"type": "exit"})
		ws.Close()
	}()

	// 阶段三：WS 读循环（输入/resize 转发），连接关闭即退出。
	ws.SetReadLimit(terminalMaxFrame)
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var f terminalFrame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		switch f.Type {
		case "input":
			b, derr := base64.StdEncoding.DecodeString(f.Data)
			if derr == nil && len(b) > 0 {
				_, _ = shell.Stdin().Write(b)
			}
		case "resize":
			_ = shell.WindowChange(f.Cols, f.Rows)
		}
	}
}

// readTerminalFrame 读取一帧 JSON。
func readTerminalFrame(ws *websocket.Conn) (*terminalFrame, error) {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	var f terminalFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, errors.New("帧格式错误")
	}
	return &f, nil
}

// terminalHostOf 取节点的 SSH 目标主机（local 或无地址节点回环本机）。
func terminalHostOf(n *store.Node) string {
	addr := strings.TrimSpace(n.Address)
	if addr == "" {
		return "127.0.0.1"
	}
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if h, _, err := net.SplitHostPort(addr); err == nil && h != "" {
		return h
	}
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr = addr[:i]
	}
	if addr == "" {
		return "127.0.0.1"
	}
	return addr
}

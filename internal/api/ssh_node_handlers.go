package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/sshx"
	"onecloud-panel/internal/store"
)

// sshInstallWait 远端安装完成后等待 Agent 自注册的最长时间。
const sshInstallWait = 120 * time.Second

// sshInstallPayload node-ssh-install 任务参数；凭据与注册令牌均为密文。
type sshInstallPayload struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	AuthMode           string `json:"auth_mode"` // password / key
	CredentialEnc      string `json:"credential_enc"`
	PassphraseEnc      string `json:"passphrase_enc"`
	HostKeyPolicy      string `json:"host_key_policy"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
	NodeName           string `json:"node_name"`
	NetworkType        string `json:"network_type"`
	TokenID            int64  `json:"token_id"`
	CommandEnc         string `json:"command_enc"`
}

// RegisterSSHTasks 注册 SSH 添加节点任务、对账器与终态清空凭据钩子。
func (a *API) RegisterSSHTasks(r *runner.Runner) {
	r.Register("node-ssh-install", a.sshInstallTask)
	r.RegisterReconciler("node-ssh-install", a.sshInstallReconcile)
	// 终态后清空 payload，避免 SSH 凭据（密文）长期留存
	r.OnFinish(func(t *store.BackgroundTask, _ string) {
		if t.Type != "node-ssh-install" {
			return
		}
		if t.Payload == "" || t.Payload == "{}" {
			return
		}
		_ = a.store.UpdateTaskPayload(t.ID, "{}")
	})
}

type sshInstallReq struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	AuthMode           string `json:"auth_mode"`
	Password           string `json:"password"`
	PrivateKey         string `json:"private_key"`
	Passphrase         string `json:"passphrase"`
	HostKeyPolicy      string `json:"host_key_policy"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
	Name               string `json:"name"`
	NetworkType        string `json:"network_type"`
}

// POST /api/nodes/ssh-install — 通过 SSH 登录目标机执行 Agent 安装。
func (a *API) sshInstallNode(w http.ResponseWriter, r *http.Request) {
	var req sshInstallReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	req.User = strings.TrimSpace(req.User)
	if req.Host == "" || req.User == "" {
		writeError(w, http.StatusBadRequest, "SSH 主机与用户名必填")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "SSH 端口非法")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.AuthMode))
	if mode == "" {
		mode = "password"
	}
	var credential string
	switch mode {
	case "password":
		if req.Password == "" {
			writeError(w, http.StatusBadRequest, "请填写 SSH 密码")
			return
		}
		credential = req.Password
	case "key":
		if strings.TrimSpace(req.PrivateKey) == "" {
			writeError(w, http.StatusBadRequest, "请填写 SSH 私钥")
			return
		}
		credential = req.PrivateKey
	default:
		writeError(w, http.StatusBadRequest, "认证方式仅支持 password/key")
		return
	}
	policy := strings.ToLower(strings.TrimSpace(req.HostKeyPolicy))
	if policy == "" {
		policy = sshx.PolicyPin
	}
	if policy != sshx.PolicyPin && policy != sshx.PolicyStrict {
		writeError(w, http.StatusBadRequest, "主机指纹策略仅支持 pin/strict")
		return
	}
	networkType := strings.TrimSpace(req.NetworkType)
	if networkType != "" && !node.IsValidNetworkType(networkType) {
		writeError(w, http.StatusBadRequest, "接入类型仅支持 lan/wireguard/public/local")
		return
	}

	uid := auth.FromContext(r.Context()).User.ID
	rawToken, tokenID, err := a.nodes.CreateToken(
		"SSH 添加："+req.Host, time.Hour, 1, &uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "注册令牌生成失败: "+err.Error())
		return
	}

	credEnc, err := a.nodes.SealSecret(credential)
	if err != nil {
		_ = a.nodes.DeleteToken(tokenID)
		writeError(w, http.StatusInternalServerError, "凭据加密失败: "+err.Error())
		return
	}
	passEnc, err := a.nodes.SealSecret(req.Passphrase)
	if err != nil {
		_ = a.nodes.DeleteToken(tokenID)
		writeError(w, http.StatusInternalServerError, "凭据加密失败: "+err.Error())
		return
	}
	cmdEnc, err := a.nodes.SealSecret(a.buildInstallCommand(r, rawToken))
	if err != nil {
		_ = a.nodes.DeleteToken(tokenID)
		writeError(w, http.StatusInternalServerError, "安装命令加密失败: "+err.Error())
		return
	}

	payload, err := json.Marshal(sshInstallPayload{
		Host: req.Host, Port: req.Port, User: req.User, AuthMode: mode,
		CredentialEnc: credEnc, PassphraseEnc: passEnc,
		HostKeyPolicy:      policy,
		HostKeyFingerprint: sshx.NormalizeFingerprint(req.HostKeyFingerprint),
		NodeName:           strings.TrimSpace(req.Name),
		NetworkType:        networkType,
		TokenID:            tokenID,
		CommandEnc:         cmdEnc,
	})
	if err != nil {
		_ = a.nodes.DeleteToken(tokenID)
		writeError(w, http.StatusInternalServerError, "任务参数构建失败")
		return
	}

	taskID, err := a.tasks.Enqueue("node-ssh-install", nil, "", string(payload), &uid)
	if err != nil {
		_ = a.nodes.DeleteToken(tokenID)
		writeError(w, http.StatusInternalServerError, "任务入队失败: "+err.Error())
		return
	}
	a.audit.Record(r, "node", "ssh_install", "node", strconv.FormatInt(taskID, 10),
		audit.ResultSuccess, audit.DetailJSON(map[string]any{
			"host": req.Host, "port": req.Port, "user": req.User, "auth_mode": mode,
		}))
	writeJSON(w, map[string]any{"task_id": taskID, "token_id": tokenID})
}

// GET /api/nodes/ssh-install?id={id} — 任务进度（节点域权限，不回传 payload）。
func (a *API) sshInstallProgress(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "任务 ID 非法")
		return
	}
	t, err := a.store.GetTask(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	if t.Type != "node-ssh-install" {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, map[string]any{
		"id": t.ID, "type": t.Type, "status": t.Status,
		"output": t.Output, "error": t.Error,
		"created_at": t.CreatedAt, "started_at": t.StartedAt, "finished_at": t.FinishedAt,
	})
}

// sshInstallTask 执行远端安装并等待 Agent 自注册。
func (a *API) sshInstallTask(ctx context.Context, w io.Writer, t *store.BackgroundTask) error {
	var p sshInstallPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil {
		return errors.New("任务参数错误")
	}
	credential, err := a.nodes.OpenSecret(p.CredentialEnc)
	if err != nil {
		return errors.New("SSH 凭据解密失败")
	}
	passphrase, _ := a.nodes.OpenSecret(p.PassphraseEnc)
	cmd, err := a.nodes.OpenSecret(p.CommandEnc)
	if err != nil {
		return errors.New("安装命令解密失败")
	}

	cfg := sshx.DialConfig{
		Host: p.Host, Port: p.Port, User: p.User,
		HostKeyPolicy:      p.HostKeyPolicy,
		HostKeyFingerprint: p.HostKeyFingerprint,
	}
	if a.dataDir != "" {
		cfg.KnownHostsFile = filepath.Join(a.dataDir, "known_hosts")
	}
	if p.AuthMode == "key" {
		cfg.PrivateKeyPEM, cfg.Passphrase = credential, passphrase
	} else {
		cfg.Password = credential
	}

	client, err := sshx.Dial(cfg, w)
	if err != nil {
		_ = a.nodes.DeleteToken(p.TokenID)
		var hk *sshx.HostKeyError
		if errors.As(err, &hk) {
			return fmt.Errorf("%w", err)
		}
		return err
	}
	defer client.Close()

	before, err := a.nodeIDSet()
	if err != nil {
		return errors.New("节点列表读取失败")
	}

	fmt.Fprintln(w, "\n[远端] 执行 Agent 安装命令：")
	if err := client.Run(ctx, "bash -lc "+sshx.Quote(cmd), w); err != nil {
		_ = a.nodes.DeleteToken(p.TokenID)
		return fmt.Errorf("远端安装失败: %w", err)
	}
	fmt.Fprintln(w, "\n[远端] 安装命令已结束，等待 Agent 自动注册…")

	n, err := a.waitNodeRegistered(ctx, w, before, sshInstallWait)
	if err != nil {
		_ = a.nodes.DeleteToken(p.TokenID)
		return fmt.Errorf("%w%s", err, a.noRegisterHint(p.Host))
	}

	name := p.NodeName
	if name == "" {
		name = n.Name
	}
	networkType := p.NetworkType
	if networkType == "" {
		networkType = a.nodes.SuggestNetworkType(p.Host)
	}
	confirmed, err := a.nodes.Confirm(n.ID, name, networkType, "", "")
	if err != nil {
		return fmt.Errorf("节点信息写入失败: %w", err)
	}
	a.audit.RecordTask(t.CreatedBy, "node", "ssh_install_done", "node",
		strconv.FormatInt(confirmed.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{
			"host": p.Host, "name": confirmed.Name, "network_type": confirmed.NetworkType,
		}))
	fmt.Fprintf(w, "\n[完成] 节点 #%d %s 已纳管（接入类型 %s，地址 %s）\n",
		confirmed.ID, confirmed.Name, confirmed.NetworkType, confirmed.Address)
	return nil
}

// sshInstallReconcile 面板重启后依据注册令牌是否被使用判定终态。
func (a *API) sshInstallReconcile(_ context.Context, t *store.BackgroundTask) (runner.Decision, error) {
	var p sshInstallPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.TokenID == 0 {
		return runner.Decision{Status: store.TaskFailure, Error: "任务参数缺失，无法对账"}, nil
	}
	tok, err := a.store.RegistrationTokenByID(p.TokenID)
	if err != nil {
		return runner.Decision{Status: store.TaskFailure, Error: "注册令牌不存在"}, nil
	}
	if tok.UsedCount > 0 {
		return runner.Decision{Status: store.TaskSuccess,
			Note: "面板重启后对账：注册令牌已被使用，判定安装成功"}, nil
	}
	return runner.Decision{Status: store.TaskFailure,
		Error: "面板重启中断，Agent 未完成注册；可重新发起 SSH 添加"}, nil
}

// nodeIDSet 当前节点 ID 集合，用于识别新注册节点。
func (a *API) nodeIDSet() (map[int64]bool, error) {
	nodes, err := a.store.ListNodes()
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(nodes))
	for i := range nodes {
		out[nodes[i].ID] = true
	}
	return out, nil
}

// waitNodeRegistered 轮询等待新节点出现。
func (a *API) waitNodeRegistered(ctx context.Context, w io.Writer,
	before map[int64]bool, timeout time.Duration) (*store.Node, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		nodes, err := a.store.ListNodes()
		if err == nil {
			for i := range nodes {
				if !before[nodes[i].ID] {
					return &nodes[i], nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待节点自动注册超时（%s）：请确认目标机可访问面板地址，"+
				"或查看目标机 journalctl -u onecloud-panel-agent", timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			fmt.Fprintf(w, "[等待] 已等待 %d 秒…\n", int(timeout.Seconds()-
				time.Until(deadline).Seconds()))
		}
	}
}

// agentStatePath 目标机 Agent 本地身份文件；非空即跳过注册（面板换址/重装后因而无法纳管）。
const agentStatePath = "/var/lib/onecloud-panel-agent/agent.json"

// noRegisterHint 补充「Agent 未产生新节点」的可操作诊断。
// 优先指出该地址已纳管的节点（同一主机不会重复注册新节点）；否则提示目标机残留旧身份。
func (a *API) noRegisterHint(host string) string {
	if n := a.nodeByAddrHost(host); n != nil {
		return fmt.Sprintf("；目标主机 %s 已纳管为节点 #%d（%s）——同一主机不会重复注册为新节点，"+
			"如需重新纳管请先在节点列表删除该节点，并在目标机删除 %s 后重试",
			host, n.ID, n.Name, agentStatePath)
	}
	return fmt.Sprintf("；若该主机此前安装过 Agent，请先删除目标机上的 %s"+
		"（残留的旧注册身份会让 Agent 跳过注册）后重试", agentStatePath)
}

// nodeByAddrHost 按地址主机部分匹配既有节点；用于 SSH 添加失败后的精确诊断，匹配不上返回 nil。
func (a *API) nodeByAddrHost(host string) *store.Node {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	nodes, err := a.store.ListNodes()
	if err != nil {
		return nil
	}
	for i := range nodes {
		addr := strings.TrimSpace(nodes[i].Address)
		if addr == "" {
			continue
		}
		if h, _, err := net.SplitHostPort(addr); err == nil {
			addr = h
		}
		if strings.EqualFold(addr, host) {
			return &nodes[i]
		}
	}
	return nil
}

// ---- 安装命令与面板对外地址 ----

// buildInstallCommand 组装 Agent 安装命令（下载地址与 --server 均带实际 scheme）。
func (a *API) buildInstallCommand(r *http.Request, rawToken string) string {
	base := a.panelScheme(r) + "://" + a.panelExternalAddr(r)
	cmd := "curl -fsSL " + base + "/install.sh | sudo bash -s -- agent" +
		" --server " + base + " --register-token " + rawToken
	if a.panelTLSInsecure() {
		// 自签证书场景：Agent 回连需跳过证书校验（安装脚本写入 OCP_INSECURE_TLS=1）
		cmd += " --insecure"
	}
	return cmd
}

// panelExternalAddr 面板对外 host:port（取自请求 Host，过滤非法字符防注入）。
func (a *API) panelExternalAddr(r *http.Request) string {
	host := sanitizeHost(r.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, a.listenPort())
	}
	return host
}

// listenPort 返回面板实际监听端口；解析失败回退 8000。
func (a *API) listenPort() string {
	if _, port, err := net.SplitHostPort(a.listenAddr); err == nil && port != "" {
		return port
	}
	return "8000"
}

// panelScheme 面板对外访问协议：优先信任反向代理头，其次读取 TLS 设置。
func (a *API) panelScheme(r *http.Request) string {
	if v := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); v == "https" {
		return "https"
	}
	if v, _, _ := a.store.GetSetting(settingTLSEnabled); v == "1" {
		return "https"
	}
	return "http"
}

// panelTLSInsecure 自签证书场景下 Agent 回连需跳过证书校验。
func (a *API) panelTLSInsecure() bool {
	mode, _, _ := a.store.GetSetting(settingTLSMode)
	return mode == "selfsigned"
}

// sanitizeHost 仅保留主机地址合法字符，避免 Host 头注入 shell 命令。
func sanitizeHost(h string) string {
	var b strings.Builder
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '.' || c == '-' || c == ':' || c == '[' || c == ']':
			b.WriteRune(c)
		}
	}
	return b.String()
}

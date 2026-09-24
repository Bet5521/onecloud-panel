package node

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/agentclient"
	"onecloud-panel/internal/docker"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
)

// onlineWindow 最近心跳窗口。
const onlineWindow = 90 * time.Second

// 合法的接入类型。
var validNetworkTypes = map[string]bool{
	"lan": true, "wireguard": true, "public": true, "local": true,
}

func isValidNetworkType(t string) bool { return validNetworkTypes[t] }

// IsValidNetworkType 判断接入类型是否合法（lan/wireguard/public/local）。
func IsValidNetworkType(t string) bool { return isValidNetworkType(t) }

// Service 节点纳管服务。
type Service struct {
	store *store.Store
	box   *secretbox.Box
}

// New 创建节点服务。
func New(s *store.Store, box *secretbox.Box) *Service {
	return &Service{store: s, box: box}
}

// ---- 注册令牌 ----

// CreateToken 生成注册令牌；返回明文（仅此一次）与 ID。
func (s *Service) CreateToken(description string, ttl time.Duration, maxUses int, createdBy *int64) (string, int64, error) {
	raw, err := randomToken()
	if err != nil {
		return "", 0, err
	}
	if maxUses <= 0 {
		maxUses = 1
	}
	t := &store.RegistrationToken{
		TokenHash:   hash(raw),
		Description: description,
		MaxUses:     maxUses,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now().Unix(),
	}
	if ttl > 0 {
		exp := time.Now().Add(ttl).Unix()
		t.ExpiresAt = &exp
	}
	id, err := s.store.CreateRegistrationToken(t)
	if err != nil {
		return "", 0, err
	}
	return raw, id, nil
}

// ListTokens 返回注册令牌（不含哈希）。
func (s *Service) ListTokens() ([]store.RegistrationToken, error) {
	return s.store.ListRegistrationTokens()
}

// DeleteToken 删除令牌。
func (s *Service) DeleteToken(id int64) error { return s.store.DeleteRegistrationToken(id) }

// ---- Agent 注册 ----

// Register 处理 Agent 首次注册；remoteAddr 为发起注册的 TCP 来源。
// 返回节点 ID 与节点长期 Token 明文。
func (s *Service) Register(req *agent.RegisterRequest, remoteAddr string) (int64, string, error) {
	tokenHash := hash(req.RegisterToken)
	// 原子领取使用额度：先查后用会因并发注册超额（一次性令牌被用多次）
	if err := s.store.ClaimRegistrationToken(tokenHash, time.Now().Unix()); err != nil {
		return 0, "", err
	}
	tok, err := s.store.GetRegistrationTokenByHash(tokenHash)
	if err != nil {
		_ = s.store.ReleaseRegistrationToken(tokenHash)
		return 0, "", errors.New("注册令牌无效")
	}
	now := time.Now().Unix()

	nodeToken, err := randomToken()
	if err != nil {
		_ = s.store.ReleaseRegistrationToken(tokenHash)
		return 0, "", err
	}

	name := "新节点"
	if req.Host != nil && req.Host.Hostname != "" {
		name = req.Host.Hostname
	}
	// 用 TCP 来源 IP + Agent 上报端口组合出可达地址（不依赖 Host 采集成功）
	address := ""
	if host := stripPort(remoteAddr); host != "" {
		port := req.AgentPort
		if port == 0 {
			port = 9000
		}
		address = net.JoinHostPort(host, strconv.Itoa(port))
	}

	n := &store.Node{
		Name:           name,
		Mode:           "remote",
		Status:         "pending",
		Address:        address,
		AgentTokenHash: hash(nodeToken),
		OwnerUserID:    tok.CreatedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if address != "" {
		n.NetworkType = s.SuggestNetworkType(address)
	}
	applyHost(n, req.Host)
	n.DockerVersion = req.DockerVersion
	if n.LastSeen == 0 {
		n.LastSeen = now
	}

	id, err := s.store.CreateNode(n)
	if err != nil {
		_ = s.store.ReleaseRegistrationToken(tokenHash)
		return 0, "", err
	}
	if err := s.saveEncryptedToken(id, nodeToken); err != nil {
		return 0, "", err
	}
	return id, nodeToken, nil
}

func (s *Service) saveEncryptedToken(id int64, token string) error {
	if s.box == nil {
		return nil
	}
	enc, err := s.box.Seal(token)
	if err != nil {
		return err
	}
	return s.store.SetNodeToken(id, hash(token), enc)
}

// SealSecret 加密敏感字符串（SSH 凭据等随任务 payload 流转，不落明文）。
func (s *Service) SealSecret(plaintext string) (string, error) {
	if s.box == nil {
		return "", errors.New("密钥盒未初始化")
	}
	return s.box.Seal(plaintext)
}

// OpenSecret 解密 SealSecret 产生的密文。
func (s *Service) OpenSecret(encoded string) (string, error) {
	if s.box == nil {
		return "", errors.New("密钥盒未初始化")
	}
	return s.box.Open(encoded)
}

// ---- 心跳 ----

// Heartbeat 处理节点心跳，返回命中的节点（供面板向 Agent 回传节点编号）。
func (s *Service) Heartbeat(req *agent.HeartbeatRequest) (*store.Node, error) {
	n, err := s.store.GetNodeByTokenHash(hash(req.Token))
	if err != nil {
		return nil, errors.New("节点 Token 无效")
	}
	if req.Host != nil {
		applyHost(n, req.Host)
		n.DockerVersion = req.DockerVersion
		n.LastSeen = time.Now().Unix()
		if err := s.store.UpdateNodeInfo(n.ID, n); err != nil {
			return nil, err
		}
		if n.Status == "pending" {
			// 心跳即证明存活；保持 pending 等管理员确认时改 active 由确认接口处理
		}
	}
	return n, nil
}

// ---- 节点操作 ----

// Confirm 确认纳管/编辑节点信息。
func (s *Service) Confirm(id int64, name, networkType, address, altAddress string) (*store.Node, error) {
	n, err := s.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		n.Name = name
	}
	if networkType != "" {
		if !isValidNetworkType(networkType) {
			return nil, errors.New("接入类型仅支持 lan/wireguard/public/local")
		}
		n.NetworkType = networkType
	}
	if address != "" {
		n.Address = address
	}
	n.AltAddress = altAddress
	n.Status = "active"
	if err := s.store.UpdateNodeConfirm(n); err != nil {
		return nil, err
	}
	return n, nil
}

// ManualAdd 手动添加节点。ownerUserID 为添加者；传 nil 表示系统级（管理员代建）。
func (s *Service) ManualAdd(name, address, token, networkType string, ownerUserID *int64) (int64, error) {
	if name == "" || address == "" || token == "" {
		return 0, errors.New("名称、地址、Token 均必填")
	}
	if networkType == "" {
		networkType = s.SuggestNetworkType(address)
	}
	if !isValidNetworkType(networkType) {
		return 0, errors.New("接入类型仅支持 lan/wireguard/public/local")
	}
	now := time.Now().Unix()
	n := store.Node{
		Name:           name,
		Mode:           "remote",
		Status:         "active",
		NetworkType:    networkType,
		Address:        address,
		AgentTokenHash: hash(token),
		OwnerUserID:    ownerUserID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	id, err := s.store.CreateNode(&n)
	if err != nil {
		return 0, err
	}
	if err := s.saveEncryptedToken(id, token); err != nil {
		return 0, err
	}
	return id, nil
}

// RotateToken 为节点生成新 Token：先通知在线 Agent，再更新面板。
// Agent 不可达时返回错误（不改库），避免失联。
func (s *Service) RotateToken(id int64) (string, error) {
	n, err := s.store.GetNode(id)
	if err != nil {
		return "", err
	}
	if n.Mode == "local" {
		return "", errors.New("本机节点无需 Token")
	}
	if n.Address == "" {
		return "", errors.New("节点缺少地址")
	}
	var oldToken string
	if enc, err := s.store.GetNodeTokenEncrypted(id); err == nil && enc != "" && s.box != nil {
		oldToken, _ = s.box.Open(enc)
	}
	if oldToken == "" {
		return "", errors.New("缺少旧 Token 明文，无法在线轮换，请重新注册节点")
	}

	newToken, err := randomToken()
	if err != nil {
		return "", err
	}

	// 先通知 Agent（旧 Token 鉴权）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agentclient.New(n.Address, oldToken).RotateToken(ctx, newToken); err != nil {
		return "", fmt.Errorf("Agent 拒绝轮换（可能离线）: %w", err)
	}
	if err := s.saveEncryptedToken(id, newToken); err != nil {
		return "", err
	}
	return newToken, nil
}

// Get 读取节点。
func (s *Service) Get(id int64) (*store.Node, error) { return s.store.GetNode(id) }

// List 全部节点。历史数据里本机节点 network_type 可能为空，读取时兜底为 local。
func (s *Service) List() ([]store.Node, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NetworkType == "" {
			if nodes[i].Mode == "local" {
				nodes[i].NetworkType = "local"
			} else {
				nodes[i].NetworkType = "lan"
			}
		}
	}
	return nodes, nil
}

// ListFiltered 按可见性过滤节点（非管理员仅能看到自己添加的节点）。
// 历史数据里本机节点 network_type 可能为空，读取时兜底为 local。
func (s *Service) ListFiltered(f store.NodeListFilter) ([]store.Node, error) {
	nodes, err := s.store.ListNodesFiltered(f)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NetworkType == "" {
			if nodes[i].Mode == "local" {
				nodes[i].NetworkType = "local"
			} else {
				nodes[i].NetworkType = "lan"
			}
		}
	}
	return nodes, nil
}

// Remove 删除节点。
func (s *Service) Remove(id int64) error { return s.store.DeleteNode(id) }

// ---- 本机节点 ----

// EnsureLocalNode 确保内置本机节点存在并刷新信息。
func (s *Service) EnsureLocalNode(host *system.HostInfo) (*store.Node, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	var local *store.Node
	for i := range nodes {
		if nodes[i].Mode == "local" {
			local = &nodes[i]
			break
		}
	}
	now := time.Now().Unix()
	dctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	dockerVer, _ := docker.DetectLocal(dctx)
	cancel()
	if local == nil {
		local = &store.Node{
			Name:        "本机",
			Mode:        "local",
			Status:      "active",
			NetworkType: "local",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		applyHost(local, host)
		local.DockerVersion = dockerVer
		id, err := s.store.CreateNode(local)
		if err != nil {
			return nil, err
		}
		local.ID = id
		return local, nil
	}
	applyHost(local, host)
	local.DockerVersion = dockerVer
	local.LastSeen = now
	local.NetworkType = "local"
	// local 节点信息更新复用心跳更新语句
	if err := s.store.UpdateNodeInfo(local.ID, local); err != nil {
		return nil, err
	}
	return local, nil
}

// ---- 网络类型建议 ----

// SuggestNetworkType 依据本机 WG 接口与地址私网/公网判断目标地址接入类型。
func (s *Service) SuggestNetworkType(address string) string {
	ip := net.ParseIP(stripBracket(address))
	if ip == nil {
		// 无法解析为 IP（如主机名）时归为局域网
		return "lan"
	}
	local, err := system.Collect()
	if err == nil {
		ifaces, err := net.Interfaces()
		if err == nil {
			for _, ifc := range ifaces {
				isWG := false
				for _, li := range local.Interfaces {
					if li.Name == ifc.Name && li.WireGuard {
						isWG = true
						break
					}
				}
				if !isWG {
					continue
				}
				addrs, err := ifc.Addrs()
				if err != nil {
					continue
				}
				for _, a := range addrs {
					if ipnet, ok := a.(*net.IPNet); ok && ipnet.Contains(ip) {
						return "wireguard"
					}
				}
			}
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return "lan"
	}
	return "public"
}

// IsOnline 根据心跳时间判断在线。
func IsOnline(lastSeen int64) bool {
	if lastSeen == 0 {
		return false
	}
	return time.Now().Unix()-lastSeen <= int64(onlineWindow.Seconds())
}

// CheckReachability TCP 探测地址可达性。
func CheckReachability(address string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ---- 辅助 ----

func applyHost(n *store.Node, h *system.HostInfo) {
	if h == nil {
		return
	}
	n.Hostname = h.Hostname
	n.OSName = h.OSName
	n.OSVersion = h.OSVersion
	n.Kernel = h.Kernel
	n.Arch = h.Arch
	n.CPUCores = h.CPUCores
	n.MemTotal = h.MemTotal
	n.LastSeen = time.Now().Unix()
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return host
}

func stripBracket(addr string) string {
	host := stripPort(addr)
	if host == "" {
		return addr
	}
	return host
}

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/store"
)

// NodeDTO 节点对外结构：附加在线/可达状态。
type NodeDTO struct {
	ID                       int64  `json:"id"`
	Name                     string `json:"name"`
	Mode                     string `json:"mode"`
	Status                   string `json:"status"`
	NetworkType              string `json:"network_type"`
	Address                  string `json:"address"`
	AltAddress               string `json:"alt_address"`
	Hostname                 string `json:"hostname"`
	OSName                   string `json:"os_name"`
	OSVersion                string `json:"os_version"`
	Kernel                   string `json:"kernel"`
	Arch                     string `json:"arch"`
	CPUCores                 int    `json:"cpu_cores"`
	MemTotal                 int64  `json:"mem_total"`
	Docker                   string `json:"docker_version"`
	DockerMirrors            string `json:"docker_mirrors"`
	DockerInsecureRegistries string `json:"docker_insecure_registries"`
	LastSeen                 int64  `json:"last_seen"`
	Online                   bool   `json:"online"`
	Reachable                bool   `json:"reachable"`
}

func toDTO(n *store.Node) NodeDTO {
	d := NodeDTO{
		ID: n.ID, Name: n.Name, Mode: n.Mode, Status: n.Status,
		NetworkType: n.NetworkType, Address: n.Address, AltAddress: n.AltAddress,
		Hostname: n.Hostname, OSName: n.OSName, OSVersion: n.OSVersion,
		Kernel: n.Kernel, Arch: n.Arch, CPUCores: n.CPUCores,
		MemTotal: n.MemTotal, Docker: n.DockerVersion,
		DockerMirrors: n.DockerMirrors, DockerInsecureRegistries: n.DockerInsecureRegistries,
		LastSeen: n.LastSeen,
		Online:   n.Mode == "local" || node.IsOnline(n.LastSeen),
	}
	if n.Address != "" {
		d.Reachable = node.CheckReachability(n.Address, 2*time.Second)
	} else if n.Mode == "local" {
		d.Reachable = true
	}
	return d
}

// ---- Agent 注册/心跳（公开） ----

func (a *API) agentRegister(w http.ResponseWriter, r *http.Request) {
	var req agent.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	id, token, err := a.nodes.Register(&req, r.RemoteAddr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, &agent.RegisterResponse{NodeID: id, Token: token})
}

func (a *API) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req agent.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.nodes.Heartbeat(&req); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, &agent.HeartbeatResponse{OK: true})
}

// ---- 节点列表/详情 ----

func (a *API) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.nodes.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]NodeDTO, 0, len(nodes))
	for i := range nodes {
		dto := toDTO(&nodes[i])
		// 筛选
		if nt := r.URL.Query().Get("network_type"); nt != "" && dto.NetworkType != nt {
			continue
		}
		if on := r.URL.Query().Get("online"); on == "1" && !dto.Online {
			continue
		}
		if on := r.URL.Query().Get("online"); on == "0" && dto.Online {
			continue
		}
		out = append(out, dto)
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

func (a *API) getNode(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := a.nodes.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, toDTO(n))
}

// GET /api/nodes/{id}/info — 实时主机信息（接口/资源水位）。
func (a *API) nodeLiveInfo(w http.ResponseWriter, r *http.Request) {
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
	h, err := a.apps.InfoFor(r.Context(), n)
	if err != nil {
		writeError(w, http.StatusBadGateway, "实时信息获取失败: "+err.Error())
		return
	}
	writeJSON(w, h)
}

// ---- 添加/编辑/删除 ----

type manualNodeReq struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Token       string `json:"token"`
	NetworkType string `json:"network_type"`
}

func (a *API) manualAddNode(w http.ResponseWriter, r *http.Request) {
	var req manualNodeReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	id, err := a.nodes.ManualAdd(req.Name, req.Address, req.Token, req.NetworkType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "create", "node", strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": req.Name, "network_type": req.NetworkType}))
	n, _ := a.nodes.Get(id)
	writeJSON(w, toDTO(n))
}

type updateNodeReq struct {
	Name        string `json:"name"`
	NetworkType string `json:"network_type"`
	Address     string `json:"address"`
	AltAddress  string `json:"alt_address"`
	Status      string `json:"status"`
}

func (a *API) updateNode(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req updateNodeReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	n, err := a.nodes.Confirm(id, req.Name, req.NetworkType, req.Address, req.AltAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Status == "disabled" {
		_ = a.store.SetNodeStatus(id, "disabled")
		n.Status = "disabled"
	} else if req.Status == "active" {
		_ = a.store.SetNodeStatus(id, "active")
		n.Status = "active"
	}
	a.audit.Record(r, "node", "update", "node", strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": n.Name, "network_type": n.NetworkType}))
	writeJSON(w, toDTO(n))
}

func (a *API) deleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.nodes.Remove(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "delete", "node", strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- 网络建议 ----

func (a *API) suggestNetwork(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("address")
	t := a.nodes.SuggestNetworkType(addr)
	writeJSON(w, map[string]string{"network_type": t})
}

// ---- 注册令牌 ----

type tokenReq struct {
	Description string `json:"description"`
	TTLHours    int    `json:"ttl_hours"`
	MaxUses     int    `json:"max_uses"`
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.TTLHours < 0 {
		writeError(w, http.StatusBadRequest, "有效期不能为负数；0 表示永久有效")
		return
	}
	uid := auth.FromContext(r.Context()).User.ID
	raw, id, err := a.nodes.CreateToken(req.Description,
		time.Duration(req.TTLHours)*time.Hour, req.MaxUses, &uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 组装 Agent 安装命令：install.sh --server 面板地址 --register-token 令牌
	cmd := a.buildInstallCommand(r, raw)

	a.audit.Record(r, "node", "create_token", "registration_token",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]any{
		"id":              id,
		"token":           raw,
		"install_command": cmd,
	})
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := a.nodes.ListTokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type tDTO struct {
		ID          int64  `json:"id"`
		Description string `json:"description"`
		ExpiresAt   *int64 `json:"expires_at"`
		MaxUses     int    `json:"max_uses"`
		UsedCount   int    `json:"used_count"`
		CreatedAt   int64  `json:"created_at"`
		Expired     bool   `json:"expired"`
	}
	out := make([]tDTO, 0, len(tokens))
	now := time.Now().Unix()
	for _, t := range tokens {
		out = append(out, tDTO{
			ID: t.ID, Description: t.Description, ExpiresAt: t.ExpiresAt,
			MaxUses: t.MaxUses, UsedCount: t.UsedCount, CreatedAt: t.CreatedAt,
			Expired: t.ExpiresAt != nil && *t.ExpiresAt < now,
		})
	}
	writeJSON(w, map[string]any{"items": out})
}

func (a *API) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.nodes.DeleteToken(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- Token 轮换 ----

func (a *API) rotateNodeToken(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newTok, err := a.nodes.RotateToken(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "rotate_token", "node",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"token": newTok})
}

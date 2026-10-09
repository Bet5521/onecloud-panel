package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/notify"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
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
	OwnerUserID              *int64 `json:"owner_user_id"`
	Online                   bool   `json:"online"`
	Reachable                bool   `json:"reachable"`
	Tags                     string                  `json:"tags"`
	Group                    string                  `json:"node_group"`
	AgentVersion             string                  `json:"agent_version"`
	Storage                  []system.StorageDevice  `json:"storage,omitempty"`
	AutoUpgrade              bool                    `json:"auto_upgrade"`
}

func toDTO(n *store.Node) NodeDTO {
	d := NodeDTO{
		ID: n.ID, Name: n.Name, Mode: n.Mode, Status: n.Status,
		NetworkType: n.NetworkType, Address: n.Address, AltAddress: n.AltAddress,
		Hostname: n.Hostname, OSName: n.OSName, OSVersion: n.OSVersion,
		Kernel: n.Kernel, Arch: n.Arch, CPUCores: n.CPUCores,
		MemTotal: n.MemTotal, Docker: n.DockerVersion,
		DockerMirrors: n.DockerMirrors, DockerInsecureRegistries: n.DockerInsecureRegistries,
		LastSeen: n.LastSeen, OwnerUserID: n.OwnerUserID,
		Tags:         n.Tags,
		Group:        n.Group,
		AgentVersion: n.AgentVersion,
		AutoUpgrade:  n.AutoUpgrade,
		Online:       n.Mode == "local" || node.IsOnline(n.LastSeen),
	}
	if n.StorageJSON != "" && n.StorageJSON != "[]" {
		var st []system.StorageDevice
		if err := json.Unmarshal([]byte(n.StorageJSON), &st); err == nil {
			d.Storage = st
		}
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
	n, err := a.nodes.Heartbeat(&req)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	// 回传节点编号：Agent 仅有 Token（无本地 agent.json）时据此确认自身身份
	writeJSON(w, &agent.HeartbeatResponse{OK: true, NodeID: n.ID})
}

// ---- 节点列表/详情 ----

func (a *API) listNodes(w http.ResponseWriter, r *http.Request) {
	ident := auth.FromContext(r.Context())
	callerID, isAdmin := callerInfo(ident)
	var filter store.NodeListFilter
	if !isAdmin {
		// 非管理员仅能看到自己添加的节点（系统级 local 节点不可见）
		filter.OwnerUserID = &callerID
		filter.IncludeSystem = false
	} else {
		filter.IncludeSystem = true
	}
	nodes, err := a.nodes.ListFiltered(filter)
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
		if g := r.URL.Query().Get("group"); g != "" && dto.Group != g {
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
	// 可见性：非管理员不能查看他人添加的节点
	if !callerIsAdmin(r) {
		uid, _ := callerID(r)
		if n.OwnerUserID == nil || *n.OwnerUserID != uid {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
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
	if !callerIsAdmin(r) {
		uid, _ := callerID(r)
		if n.OwnerUserID == nil || *n.OwnerUserID != uid {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
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
	var ownerID *int64
	if ident := auth.FromContext(r.Context()); ident != nil && ident.User != nil {
		v := ident.User.ID
		ownerID = &v
	}
	id, err := a.nodes.ManualAdd(req.Name, req.Address, req.Token, req.NetworkType, ownerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "create", "node", strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": req.Name, "network_type": req.NetworkType}))
	a.EmitEvent(notify.EventNodeChange, "节点已添加",
		fmt.Sprintf("节点「%s」已加入集群。", req.Name))
	n, _ := a.nodes.Get(id)
	writeJSON(w, toDTO(n))
}

type updateNodeReq struct {
	Name        string  `json:"name"`
	NetworkType string  `json:"network_type"`
	Address     string  `json:"address"`
	AltAddress  string  `json:"alt_address"`
	Status      string  `json:"status"`
	Tags        *string `json:"tags"`
	Group       *string `json:"node_group"`
	AutoUpgrade *bool   `json:"auto_upgrade"`
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
	// 属主断言：非管理员仅可编辑自己添加的节点（404 掩蔽）
	own, gerr := a.nodes.Get(id)
	if gerr != nil {
		writeError(w, http.StatusNotFound, gerr.Error())
		return
	}
	if !assertNodeOwner(w, r, own) {
		return
	}
	n := own
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// 仅当修改基本信息时才走 Confirm（会刷新激活状态）；仅改标签/分组时不触碰状态
	if req.Name != "" || req.NetworkType != "" || req.Address != "" || req.AltAddress != "" || req.Status != "" {
		n, err = a.nodes.Confirm(id, req.Name, req.NetworkType, req.Address, req.AltAddress)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Status == "disabled" {
		_ = a.store.SetNodeStatus(id, "disabled")
		n.Status = "disabled"
	} else if req.Status == "active" {
		_ = a.store.SetNodeStatus(id, "active")
		n.Status = "active"
	}
	if req.Tags != nil {
		if err := a.store.SetNodeTags(id, *req.Tags); err == nil {
			n.Tags = *req.Tags
		}
	}
	if req.Group != nil {
		if err := a.store.SetNodeGroup(id, *req.Group); err == nil {
			n.Group = *req.Group
		}
	}
	if req.AutoUpgrade != nil {
		if err := a.store.SetNodeAutoUpgrade(id, *req.AutoUpgrade); err == nil {
			n.AutoUpgrade = *req.AutoUpgrade
		}
	}
	a.audit.Record(r, "node", "update", "node", strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": n.Name, "network_type": n.NetworkType}))
	a.EmitEvent(notify.EventNodeChange, "节点已更新",
		fmt.Sprintf("节点「%s」信息已更新。", n.Name))
	writeJSON(w, toDTO(n))
}

func (a *API) deleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, gerr := a.nodes.Get(id)
	if gerr != nil {
		writeError(w, http.StatusNotFound, gerr.Error())
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	nodeName := n.Name
	if err := a.nodes.Remove(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "delete", "node", strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	a.EmitEvent(notify.EventNodeChange, "节点已移除",
		fmt.Sprintf("节点「%s」已从集群移除。", nodeName))
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- 批量操作 ----

type batchNodeReq struct {
	IDs    []int64 `json:"ids"`
	Action string  `json:"action"`
	Group  string  `json:"group"`
	Tags   string  `json:"tags"`
}

func (a *API) batchNodes(w http.ResponseWriter, r *http.Request) {
	var req batchNodeReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	n, err := a.nodes.BatchOp(req.IDs, req.Action, req.Group, req.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "batch_"+req.Action, "node", "", audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"count": n}))
	writeJSON(w, map[string]any{"affected": n})
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
	n, gerr := a.nodes.Get(id)
	if gerr != nil {
		writeError(w, http.StatusNotFound, gerr.Error())
		return
	}
	if !assertNodeOwner(w, r, n) {
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

// ---- 节点 Agent 自升级 ----

// POST /api/nodes/{id}/upgrade — 手动触发的节点 Agent 自升级。
// 面板将自身正在运行的二进制作为升级包推送至节点 Agent，Agent 下载替换后重启自身单元。
func (a *API) upgradeNode(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.nodes.UpgradeAgent(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "node", "upgrade", "node", strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "upgrade_started"})
}

// GET /api/agent-binary — 向节点 Agent 提供面板自身二进制作为升级包。
// 鉴权使用节点长期 Token（查询参数 t），无需登录会话（Agent 侧无会话态）。
func (a *API) agentBinary(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		writeError(w, http.StatusUnauthorized, "缺少 Token")
		return
	}
	if _, err := a.store.GetNodeByToken(tok); err != nil {
		writeError(w, http.StatusUnauthorized, "Token 无效")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "定位二进制失败")
		return
	}
	f, err := os.Open(exe)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取二进制失败")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取二进制信息失败")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.Header().Set("Content-Disposition", "attachment; filename=\"onecloud-panel-agent\"")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("agent-binary 下载写出失败: %v", err)
	}
}

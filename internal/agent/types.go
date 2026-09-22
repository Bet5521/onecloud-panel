package agent

import "onecloud-panel/internal/system"

// ---- 注册 / 心跳（Agent → Panel） ----

// RegisterRequest Agent 首次注册请求。
type RegisterRequest struct {
	RegisterToken string           `json:"register_token"`
	AgentPort     int              `json:"agent_port"`
	Host          *system.HostInfo `json:"host"`
	DockerVersion string           `json:"docker_version,omitempty"`
}

// RegisterResponse 面板返回的长期 Token。
type RegisterResponse struct {
	NodeID int64  `json:"node_id"`
	Token  string `json:"token"`
}

// HeartbeatRequest 周期心跳。
type HeartbeatRequest struct {
	Token         string           `json:"token"`
	Host          *system.HostInfo `json:"host"`
	DockerVersion string           `json:"docker_version,omitempty"`
}

// HeartbeatResponse 心跳应答；RotateToken 非空时 Agent 切换 Token。
//
// NodeID 让 Agent 在无本地身份时（install.sh 的 --token 重装路径）也能确认
// 自身节点编号，避免启动日志与首次心跳前显示「节点 0」。
type HeartbeatResponse struct {
	OK          bool   `json:"ok"`
	RotateToken string `json:"rotate_token,omitempty"`
	NodeID      int64  `json:"node_id,omitempty"`
}

// ---- 节点操作（Panel → Agent） ----

const (
	authHeader = "Authorization"
	bearer     = "Bearer "
)

// ExecReq 命令执行。
type ExecReq struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// ExecResp 执行结果。
type ExecResp struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// SystemctlReq systemd 操作。
type SystemctlReq struct {
	Action string `json:"action"` // start/stop/restart/enable/disable/status/is-active
	Unit   string `json:"unit"`
}

// FileReq 文件读写。二进制内容必须走 ContentB64，避免 JSON 字符串对非法
// UTF-8 字节的替换（\ufffd）导致内容损坏（优先于 Content）。
type FileReq struct {
	Path       string `json:"path"`
	Content    string `json:"content,omitempty"`
	ContentB64 string `json:"content_b64,omitempty"`
}

// FileResp 文件内容。二进制通过 ContentB64 传输；Content 仅在内容为合法
// UTF-8 时填充，供人类可读的文本配置使用。
type FileResp struct {
	Path       string `json:"path"`
	Content    string `json:"content,omitempty"`
	ContentB64 string `json:"content_b64,omitempty"`
}

// DownloadReq 下载到节点。
type DownloadReq struct {
	URL    string `json:"url"`
	Dest   string `json:"dest"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}

// DownloadResp 下载结果。
type DownloadResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// HealthcheckReq 节点本机健康探测。
type HealthcheckReq struct {
	Type string `json:"type"`
	Port int    `json:"port"`
	Path string `json:"path"`
}

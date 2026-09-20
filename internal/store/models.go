package store

import "time"

// Role 角色。
type Role struct {
	ID        int64
	Code      string
	Name      string
	Builtin   bool
	Protected bool
	CreatedAt int64
	UpdatedAt int64
}

// User 用户。
type User struct {
	ID           int64
	Username     string
	RealName     string
	Phone        string
	PasswordHash string
	SuperCode    string
	RoleID       int64
	Status       string
	// 通知方式：log / email / sms / channel
	NotifyMethod    string
	NotifyEmail     string
	NotifySMSPhone  string
	NotifyChannelID *int64
	CreatedAt       int64
	UpdatedAt       int64
}

// Node 集群节点。
type Node struct {
	ID                       int64
	Name                     string
	Mode                     string // local / remote
	Status                   string // pending / active / disabled
	NetworkType              string // lan / wireguard / public / local
	Address                  string
	AltAddress               string
	AgentTokenHash           string
	Hostname                 string
	OSName                   string
	OSVersion                string
	Kernel                   string
	Arch                     string
	CPUCores                 int
	MemTotal                 int64
	DockerVersion            string
	DockerMirrors            string
	DockerInsecureRegistries string
	LastSeen                 int64
	CreatedAt                int64
	UpdatedAt                int64
}

// RegistrationToken 节点注册令牌（仅存哈希）。
type RegistrationToken struct {
	ID          int64
	TokenHash   string
	Description string
	ExpiresAt   *int64
	MaxUses     int
	UsedCount   int
	CreatedBy   *int64
	CreatedAt   int64
}

// AuditLog 审计日志。
type AuditLog struct {
	ID         int64
	UserID     *int64
	Username   string
	Ts         int64
	IP         string
	Module     string
	Action     string
	TargetType string
	TargetID   string
	Result     string
	RequestID  string
	Detail     string
}

// AppInstallation 应用安装记录。
type AppInstallation struct {
	ID            int64
	NodeID        int64
	AppID         string
	Method        string
	Status        string
	Params        string
	ServiceName   string
	ContainerID   string
	ContainerName string
	InstalledAt   int64
	UpdatedAt     int64
}

// BackgroundTask 后台任务。
type BackgroundTask struct {
	ID         int64
	Type       string
	Status     string
	NodeID     *int64
	AppID      string
	Payload    string
	Output     string
	Error      string
	CreatedBy  *int64
	CreatedAt  int64
	StartedAt  *int64
	FinishedAt *int64
}

func now() int64 { return time.Now().Unix() }

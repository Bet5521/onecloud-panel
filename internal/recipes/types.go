package recipes

// Recipe 应用配方（YAML 可序列化结构）。
type Recipe struct {
	APIVersion  int          `yaml:"api_version"`
	ID          string       `yaml:"id"`
	Name        string       `yaml:"name"`
	Category    string       `yaml:"category"`
	Icon        string       `yaml:"icon"`
	Description string       `yaml:"description"`
	Homepage    string       `yaml:"homepage"`
	Methods     []string     `yaml:"methods"`
	Ports       []PortSpec   `yaml:"ports"`
	Volumes     []string     `yaml:"volumes"`
	ConfigFiles []ConfigFile `yaml:"config_files"`
	Variables   []Variable   `yaml:"variables"`
	Healthcheck *Healthcheck `yaml:"healthcheck"`
	Native      *NativeSpec  `yaml:"native"`
	Docker      *DockerSpec  `yaml:"docker"`
}

// PortSpec 端口声明。
type PortSpec struct {
	Port        int    `yaml:"port"`
	Proto       string `yaml:"proto"`
	Description string `yaml:"description"`
}

// ConfigFile 面板可编辑的配置文件白名单项。
type ConfigFile struct {
	Path        string `yaml:"path"`
	Optional    bool   `yaml:"optional"`
	Description string `yaml:"description"`
}

// Variable 用户可填变量。
type Variable struct {
	Key         string   `yaml:"key"`
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"` // string/int/bool/select
	Default     string   `yaml:"default"`
	Required    bool     `yaml:"required"`
	Description string   `yaml:"description"`
	Options     []string `yaml:"options"`
}

// Healthcheck 健康检查。
type Healthcheck struct {
	Type    string   `yaml:"type"` // http/tcp/command
	Port    int      `yaml:"port"`
	Path    string   `yaml:"path"`
	Command []string `yaml:"command"`
}

// NativeSpec 直装方式规格。
type NativeSpec struct {
	Arches         []string      `yaml:"arches"`
	Packages       []string      `yaml:"packages"`
	Download       *DownloadSpec `yaml:"download"`
	UnitName       string        `yaml:"unit_name"`
	UnitTemplate   string        `yaml:"unit_template"`
	InstallSteps   []Step        `yaml:"install_steps"`
	UninstallSteps []Step        `yaml:"uninstall_steps"`
}

// DownloadSpec 二进制下载。
type DownloadSpec struct {
	URLTemplate string            `yaml:"url_template"`
	SHA256      map[string]string `yaml:"sha256"` // arch → 校验值（可空）
	Dest        string            `yaml:"dest"`
	Mode        uint32            `yaml:"mode"`
}

// Step 单个安装/卸载步骤；同一时间仅允许一种动作。
type Step struct {
	Name      string     `yaml:"name"`
	Apt       []string   `yaml:"apt"`
	Download  *Download  `yaml:"download"`
	Mkdir     []string   `yaml:"mkdir"`
	Write     *WriteFile `yaml:"write"`
	Exec      *ExecStep  `yaml:"exec"`
	Systemctl *Systemctl `yaml:"systemctl"`
}

// Download 步骤内下载（区别于 NativeSpec.Download 主程序）。
type Download struct {
	URL  string `yaml:"url"`
	Dest string `yaml:"dest"`
	Mode uint32 `yaml:"mode"`
}

// WriteFile 渲染并写入文件。
type WriteFile struct {
	Path    string `yaml:"path"`
	Mode    uint32 `yaml:"mode"`
	Content string `yaml:"content"`
}

// ExecStep 执行命令。
type ExecStep struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// Systemctl systemd 操作。
type Systemctl struct {
	Action string `yaml:"action"`
	Unit   string `yaml:"unit"`
}

// DockerSpec 容器方式规格。
type DockerSpec struct {
	Arches []string `yaml:"arches"`
	Image  string   `yaml:"image"`
	// User 指定容器内运行身份（UID 或 UID:GID）。部分镜像已移除 PUID/PGID
	// 环境变量，例如 OpenList v4.1.0+ 固定以 openlist(1001) 运行并废弃了
	// PUID/PGID，此时只能靠本字段以 root 运行才能写入宿主绑定目录。
	User           string   `yaml:"user"`
	Ports          []string `yaml:"ports"`
	Volumes        []string `yaml:"volumes"`
	Env            []string `yaml:"env"`
	Privileged     bool     `yaml:"privileged"`
	NetworkMode    string   `yaml:"network_mode"`
	RestartPolicy  string   `yaml:"restart_policy"`
	InstallSteps   []Step   `yaml:"install_steps"` // 容器外的前置准备（目录等）
	UninstallSteps []Step   `yaml:"uninstall_steps"`
}

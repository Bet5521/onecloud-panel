package store

import (
	"database/sql"
	"fmt"
)

// CustomApp 自定义应用。
type CustomApp struct {
	ID          int64  `json:"id"`
	AppID       string `json:"app_id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	Method      string `json:"method"` // native / docker

	// 通用（JSON 字符串）
	Ports     string `json:"ports"`
	Variables string `json:"variables"`

	// native
	DownloadURL     string `json:"download_url"`
	UnitName        string `json:"unit_name"`
	UnitTemplate    string `json:"unit_template"`
	InstallScript   string `json:"install_script"`
	UninstallScript string `json:"uninstall_script"`

	// docker
	DockerImage     string `json:"docker_image"`
	DockerPorts     string `json:"docker_ports"`
	DockerVolumes   string `json:"docker_volumes"`
	DockerEnv       string `json:"docker_env"`
	DockerNetwork   string `json:"docker_network"`
	DockerRestart   string `json:"docker_restart"`
	DockerPrivileged bool  `json:"docker_privileged"`

	// 健康检查
	HealthcheckType string `json:"healthcheck_type"`
	HealthcheckPort int    `json:"healthcheck_port"`
	HealthcheckPath string `json:"healthcheck_path"`
	HealthcheckCmd  string `json:"healthcheck_cmd"`

	// 元数据
	CreatedBy *int64 `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// CreateCustomApp 创建自定义应用。
func (s *Store) CreateCustomApp(app *CustomApp) (int64, error) {
	t := now()
	app.CreatedAt = t
	app.UpdatedAt = t
	if app.Method == "" {
		app.Method = "docker"
	}
	res, err := s.DB.Exec(
		`INSERT INTO custom_apps
		 (app_id, name, category, icon, description, homepage, method,
		  ports, variables,
		  download_url, unit_name, unit_template, install_script, uninstall_script,
		  docker_image, docker_ports, docker_volumes, docker_env,
		  docker_network, docker_restart, docker_privileged,
		  healthcheck_type, healthcheck_port, healthcheck_path, healthcheck_cmd,
		  created_by, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		app.AppID, app.Name, app.Category, app.Icon, app.Description, app.Homepage, app.Method,
		app.Ports, app.Variables,
		app.DownloadURL, app.UnitName, app.UnitTemplate, app.InstallScript, app.UninstallScript,
		app.DockerImage, app.DockerPorts, app.DockerVolumes, app.DockerEnv,
		app.DockerNetwork, app.DockerRestart, boolToInt(app.DockerPrivileged),
		app.HealthcheckType, app.HealthcheckPort, app.HealthcheckPath, app.HealthcheckCmd,
		app.CreatedBy, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetCustomApp 按 ID 查询自定义应用。
func (s *Store) GetCustomApp(id int64) (*CustomApp, error) {
	row := &CustomApp{}
	var priv int
	err := s.DB.QueryRow(
		`SELECT id, app_id, name, category, icon, description, homepage, method,
		        ports, variables,
		        download_url, unit_name, unit_template, install_script, uninstall_script,
		        docker_image, docker_ports, docker_volumes, docker_env,
		        docker_network, docker_restart, docker_privileged,
		        healthcheck_type, healthcheck_port, healthcheck_path, healthcheck_cmd,
		        created_by, created_at, updated_at
		 FROM custom_apps WHERE id=?`, id).Scan(
		&row.ID, &row.AppID, &row.Name, &row.Category, &row.Icon, &row.Description,
		&row.Homepage, &row.Method,
		&row.Ports, &row.Variables,
		&row.DownloadURL, &row.UnitName, &row.UnitTemplate, &row.InstallScript, &row.UninstallScript,
		&row.DockerImage, &row.DockerPorts, &row.DockerVolumes, &row.DockerEnv,
		&row.DockerNetwork, &row.DockerRestart, &priv,
		&row.HealthcheckType, &row.HealthcheckPort, &row.HealthcheckPath, &row.HealthcheckCmd,
		&row.CreatedBy, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	row.DockerPrivileged = priv != 0
	return row, nil
}

// GetCustomAppByAppID 按 app_id 查询自定义应用。
func (s *Store) GetCustomAppByAppID(appID string) (*CustomApp, error) {
	row := &CustomApp{}
	var priv int
	err := s.DB.QueryRow(
		`SELECT id, app_id, name, category, icon, description, homepage, method,
		        ports, variables,
		        download_url, unit_name, unit_template, install_script, uninstall_script,
		        docker_image, docker_ports, docker_volumes, docker_env,
		        docker_network, docker_restart, docker_privileged,
		        healthcheck_type, healthcheck_port, healthcheck_path, healthcheck_cmd,
		        created_by, created_at, updated_at
		 FROM custom_apps WHERE app_id=?`, appID).Scan(
		&row.ID, &row.AppID, &row.Name, &row.Category, &row.Icon, &row.Description,
		&row.Homepage, &row.Method,
		&row.Ports, &row.Variables,
		&row.DownloadURL, &row.UnitName, &row.UnitTemplate, &row.InstallScript, &row.UninstallScript,
		&row.DockerImage, &row.DockerPorts, &row.DockerVolumes, &row.DockerEnv,
		&row.DockerNetwork, &row.DockerRestart, &priv,
		&row.HealthcheckType, &row.HealthcheckPort, &row.HealthcheckPath, &row.HealthcheckCmd,
		&row.CreatedBy, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	row.DockerPrivileged = priv != 0
	return row, nil
}

// ListCustomApps 列出全部自定义应用。
func (s *Store) ListCustomApps() ([]CustomApp, error) {
	rows, err := s.DB.Query(
		`SELECT id, app_id, name, category, icon, description, homepage, method,
		        ports, variables,
		        download_url, unit_name, unit_template, install_script, uninstall_script,
		        docker_image, docker_ports, docker_volumes, docker_env,
		        docker_network, docker_restart, docker_privileged,
		        healthcheck_type, healthcheck_port, healthcheck_path, healthcheck_cmd,
		        created_by, created_at, updated_at
		 FROM custom_apps ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomApp
	for rows.Next() {
		var app CustomApp
		var priv int
		if err := rows.Scan(
			&app.ID, &app.AppID, &app.Name, &app.Category, &app.Icon, &app.Description,
			&app.Homepage, &app.Method,
			&app.Ports, &app.Variables,
			&app.DownloadURL, &app.UnitName, &app.UnitTemplate, &app.InstallScript, &app.UninstallScript,
			&app.DockerImage, &app.DockerPorts, &app.DockerVolumes, &app.DockerEnv,
			&app.DockerNetwork, &app.DockerRestart, &priv,
			&app.HealthcheckType, &app.HealthcheckPort, &app.HealthcheckPath, &app.HealthcheckCmd,
			&app.CreatedBy, &app.CreatedAt, &app.UpdatedAt); err != nil {
			return nil, err
		}
		app.DockerPrivileged = priv != 0
		out = append(out, app)
	}
	return out, rows.Err()
}

// UpdateCustomApp 更新自定义应用。
func (s *Store) UpdateCustomApp(app *CustomApp) error {
	app.UpdatedAt = now()
	_, err := s.DB.Exec(
		`UPDATE custom_apps SET
		 name=?, category=?, icon=?, description=?, homepage=?, method=?,
		 ports=?, variables=?,
		 download_url=?, unit_name=?, unit_template=?, install_script=?, uninstall_script=?,
		 docker_image=?, docker_ports=?, docker_volumes=?, docker_env=?,
		 docker_network=?, docker_restart=?, docker_privileged=?,
		 healthcheck_type=?, healthcheck_port=?, healthcheck_path=?, healthcheck_cmd=?,
		 updated_at=?
		 WHERE id=?`,
		app.Name, app.Category, app.Icon, app.Description, app.Homepage, app.Method,
		app.Ports, app.Variables,
		app.DownloadURL, app.UnitName, app.UnitTemplate, app.InstallScript, app.UninstallScript,
		app.DockerImage, app.DockerPorts, app.DockerVolumes, app.DockerEnv,
		app.DockerNetwork, app.DockerRestart, boolToInt(app.DockerPrivileged),
		app.HealthcheckType, app.HealthcheckPort, app.HealthcheckPath, app.HealthcheckCmd,
		app.UpdatedAt, app.ID)
	return err
}

// DeleteCustomApp 删除自定义应用。
func (s *Store) DeleteCustomApp(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM custom_apps WHERE id=?`, id)
	return err
}

// CountCustomApps 统计自定义应用数量。
func (s *Store) CountCustomApps() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM custom_apps`).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CustomAppExists 检查 app_id 是否已存在。
func (s *Store) CustomAppExists(appID string) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM custom_apps WHERE app_id=?`, appID).Scan(&n)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("查询自定义应用失败: %w", err)
	}
	return n > 0, nil
}
package store

// CreateInstallation 写入安装记录。
func (s *Store) CreateInstallation(in *AppInstallation) (int64, error) {
	t := now()
	in.InstalledAt = t
	in.UpdatedAt = t
	if in.Status == "" {
		in.Status = "installed"
	}
	res, err := s.DB.Exec(
		`INSERT INTO app_installations
		 (node_id, app_id, method, status, params, service_name,
		  container_id, container_name, installed_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.NodeID, in.AppID, in.Method, in.Status, in.Params, in.ServiceName,
		in.ContainerID, in.ContainerName, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetInstallation 按节点+应用查询。
func (s *Store) GetInstallation(nodeID int64, appID string) (*AppInstallation, error) {
	row := &AppInstallation{}
	err := s.DB.QueryRow(
		`SELECT id, node_id, app_id, method, status, params, service_name,
		        container_id, container_name, installed_at, updated_at
		 FROM app_installations WHERE node_id=? AND app_id=?`,
		nodeID, appID).Scan(
		&row.ID, &row.NodeID, &row.AppID, &row.Method, &row.Status, &row.Params,
		&row.ServiceName, &row.ContainerID, &row.ContainerName,
		&row.InstalledAt, &row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ListInstallations 按节点列出安装记录。
func (s *Store) ListInstallations(nodeID int64) ([]AppInstallation, error) {
	rows, err := s.DB.Query(
		`SELECT id, node_id, app_id, method, status, params, service_name,
		        container_id, container_name, installed_at, updated_at
		 FROM app_installations WHERE node_id=? ORDER BY id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppInstallation
	for rows.Next() {
		var in AppInstallation
		if err := rows.Scan(
			&in.ID, &in.NodeID, &in.AppID, &in.Method, &in.Status, &in.Params,
			&in.ServiceName, &in.ContainerID, &in.ContainerName,
			&in.InstalledAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ListAllInstallations 返回全部节点的安装记录（仪表盘聚合用）。
func (s *Store) ListAllInstallations() ([]AppInstallation, error) {
	rows, err := s.DB.Query(
		`SELECT id, node_id, app_id, method, status, params, service_name,
		        container_id, container_name, installed_at, updated_at
		 FROM app_installations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppInstallation
	for rows.Next() {
		var in AppInstallation
		if err := rows.Scan(
			&in.ID, &in.NodeID, &in.AppID, &in.Method, &in.Status, &in.Params,
			&in.ServiceName, &in.ContainerID, &in.ContainerName,
			&in.InstalledAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// UpsertInstallation 安装记录不存在则创建（面板重启对账时补建崩溃窗口内丢失的记录）。
func (s *Store) UpsertInstallation(in *AppInstallation) error {
	if existing, err := s.GetInstallation(in.NodeID, in.AppID); err == nil && existing != nil {
		return nil
	}
	_, err := s.CreateInstallation(in)
	return err
}

// UpdateInstallationStatus 更新状态。
func (s *Store) UpdateInstallationStatus(nodeID int64, appID, status string) error {
	_, err := s.DB.Exec(
		`UPDATE app_installations SET status=?, updated_at=? WHERE node_id=? AND app_id=?`,
		status, now(), nodeID, appID)
	return err
}

// DeleteInstallation 删除安装记录。
func (s *Store) DeleteInstallation(nodeID int64, appID string) error {
	_, err := s.DB.Exec(
		`DELETE FROM app_installations WHERE node_id=? AND app_id=?`, nodeID, appID)
	return err
}

package store

import (
	"database/sql"
	"errors"
)

// ErrNodeNotFound 节点不存在。
var ErrNodeNotFound = errors.New("节点不存在")

// CreateNode 插入节点，返回 ID。
func (s *Store) CreateNode(n *Node) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO nodes
		 (name, mode, status, network_type, address, alt_address, agent_token_hash,
		  hostname, os_name, os_version, kernel, arch, cpu_cores, mem_total, docker_version,
		  docker_mirrors, docker_insecure_registries, last_seen, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Mode, n.Status, n.NetworkType, n.Address, n.AltAddress,
		n.AgentTokenHash, n.Hostname, n.OSName, n.OSVersion, n.Kernel, n.Arch,
		n.CPUCores, n.MemTotal, n.DockerVersion,
		n.DockerMirrors, n.DockerInsecureRegistries, n.LastSeen, n.CreatedAt, n.UpdatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetNode 按 ID 查询。
func (s *Store) GetNode(id int64) (*Node, error) {
	return s.node("WHERE id = ?", id)
}

// GetNodeByTokenHash 按 Agent Token 哈希查询。
func (s *Store) GetNodeByTokenHash(hash string) (*Node, error) {
	return s.node("WHERE agent_token_hash = ?", hash)
}

// ListNodes 全部节点（mode 优先 local 在前，其余按 id）。
func (s *Store) ListNodes() ([]Node, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, mode, status, network_type, address, alt_address, agent_token_hash,
		        hostname, os_name, os_version, kernel, arch, cpu_cores, mem_total, docker_version,
		        docker_mirrors, docker_insecure_registries, last_seen, created_at, updated_at
		 FROM nodes ORDER BY (mode = 'local') DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// UpdateNodeConfirm 确认/编辑节点基本信息。
func (s *Store) UpdateNodeConfirm(n *Node) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET name=?, status=?, network_type=?, address=?, alt_address=?, updated_at=?
		 WHERE id=?`,
		n.Name, n.Status, n.NetworkType, n.Address, n.AltAddress, now(), n.ID)
	return err
}

// UpdateNodeInfo 心跳更新主机信息与最近在线时间。
// network_type 仅在传入非空时覆盖，避免心跳路径未载入该字段时被清空。
func (s *Store) UpdateNodeInfo(id int64, n *Node) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET hostname=?, os_name=?, os_version=?, kernel=?, arch=?,
		   cpu_cores=?, mem_total=?, docker_version=?, last_seen=?,
		   network_type = CASE WHEN ? <> '' THEN ? ELSE network_type END,
		   updated_at=?
		 WHERE id=?`,
		n.Hostname, n.OSName, n.OSVersion, n.Kernel, n.Arch,
		n.CPUCores, n.MemTotal, n.DockerVersion, n.LastSeen,
		n.NetworkType, n.NetworkType, now(), id)
	return err
}

// SetNodeDockerVersion 更新节点 Docker 版本（空串=未安装）。
func (s *Store) SetNodeDockerVersion(id int64, version string) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET docker_version=?, updated_at=? WHERE id=?`, version, now(), id)
	return err
}

// SetNodeToken 更新 Agent Token 哈希与密文。
func (s *Store) SetNodeToken(id int64, tokenHash, tokenEnc string) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET agent_token_hash=?, agent_token_enc=?, updated_at=? WHERE id=?`,
		tokenHash, tokenEnc, now(), id)
	return err
}

// GetNodeTokenEncrypted 返回节点 Token 密文（轮换用）。
func (s *Store) GetNodeTokenEncrypted(id int64) (string, error) {
	var enc sql.NullString
	if err := s.DB.QueryRow(
		`SELECT agent_token_enc FROM nodes WHERE id=?`, id).Scan(&enc); err != nil {
		return "", err
	}
	return enc.String, nil
}

// SetNodeStatus 修改节点状态。
func (s *Store) SetNodeStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE nodes SET status=?, updated_at=? WHERE id=?`, status, now(), id)
	return err
}

// DeleteNode 删除节点（级联删除安装记录）。
func (s *Store) DeleteNode(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM nodes WHERE id=? AND mode != 'local'`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) node(where string, args ...any) (*Node, error) {
	q := `SELECT id, name, mode, status, network_type, address, alt_address, agent_token_hash,
	             hostname, os_name, os_version, kernel, arch, cpu_cores, mem_total, docker_version,
	             docker_mirrors, docker_insecure_registries, last_seen, created_at, updated_at
	      FROM nodes ` + where
	n := &Node{}
	err := s.DB.QueryRow(q, args...).Scan(
		&n.ID, &n.Name, &n.Mode, &n.Status, &n.NetworkType, &n.Address, &n.AltAddress,
		&n.AgentTokenHash, &n.Hostname, &n.OSName, &n.OSVersion, &n.Kernel, &n.Arch,
		&n.CPUCores, &n.MemTotal, &n.DockerVersion,
		&n.DockerMirrors, &n.DockerInsecureRegistries, &n.LastSeen, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

type scanner interface {
	Scan(dest ...any) error
	Next() bool
	Err() error
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(
			&n.ID, &n.Name, &n.Mode, &n.Status, &n.NetworkType, &n.Address, &n.AltAddress,
			&n.AgentTokenHash, &n.Hostname, &n.OSName, &n.OSVersion, &n.Kernel, &n.Arch,
			&n.CPUCores, &n.MemTotal, &n.DockerVersion,
			&n.DockerMirrors, &n.DockerInsecureRegistries, &n.LastSeen,
			&n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UpdateNodeDockerConfig 更新节点 Docker 镜像加速与第三方仓库配置。
func (s *Store) UpdateNodeDockerConfig(id int64, mirrors, insecure string) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET docker_mirrors=?, docker_insecure_registries=?, updated_at=? WHERE id=?`,
		mirrors, insecure, now(), id)
	return err
}

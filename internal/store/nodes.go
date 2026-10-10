package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
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
		  docker_mirrors, docker_insecure_registries, agent_version, storage_json, upgrade_json,
		  auto_upgrade, last_seen, owner_user_id, tags, node_group,
		  created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Mode, n.Status, n.NetworkType, n.Address, n.AltAddress,
		n.AgentTokenHash, n.Hostname, n.OSName, n.OSVersion, n.Kernel, n.Arch,
		n.CPUCores, n.MemTotal, n.DockerVersion,
		n.DockerMirrors, n.DockerInsecureRegistries, n.AgentVersion, n.StorageJSON, n.UpgradeJSON,
		b2i(n.AutoUpgrade),
		n.LastSeen, n.OwnerUserID, n.Tags, n.Group,
		n.CreatedAt, n.UpdatedAt)
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
		        docker_mirrors, docker_insecure_registries, agent_version, storage_json, upgrade_json,
		        auto_upgrade, last_seen, owner_user_id, tags, node_group,
		        created_at, updated_at
		 FROM nodes ORDER BY (mode = 'local') DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// NodeListFilter 节点可见性过滤。
type NodeListFilter struct {
	// OwnerUserID 非 nil 时仅返回该用户添加的节点。
	OwnerUserID *int64
	// IncludeSystem 是否包含 owner_user_id IS NULL 的系统节点（如 local）。
	IncludeSystem bool
}

// ListNodesFiltered 按归属过滤节点；用于非管理员只能看自己添加的节点。
func (s *Store) ListNodesFiltered(f NodeListFilter) ([]Node, error) {
	q := `SELECT id, name, mode, status, network_type, address, alt_address, agent_token_hash,
		        hostname, os_name, os_version, kernel, arch, cpu_cores, mem_total, docker_version,
		        docker_mirrors, docker_insecure_registries, agent_version, storage_json, upgrade_json,
		        auto_upgrade, last_seen, owner_user_id, tags, node_group, created_at, updated_at
		  FROM nodes`
	var conds []string
	var args []any
	if f.OwnerUserID != nil {
		conds = append(conds, "owner_user_id = ?")
		args = append(args, *f.OwnerUserID)
	}
	if !f.IncludeSystem {
		conds = append(conds, "owner_user_id IS NOT NULL")
	}
	if len(conds) > 0 {
		q += " WHERE " + joinAnd(conds)
	}
	q += " ORDER BY (mode = 'local') DESC, id ASC"
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

func joinAnd(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

// UpdateNodeConfirm 确认/编辑节点基本信息。
func (s *Store) UpdateNodeConfirm(n *Node) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET name=?, status=?, network_type=?, address=?, alt_address=?,
		   tags=?, node_group=?, updated_at=?
		 WHERE id=?`,
		n.Name, n.Status, n.NetworkType, n.Address, n.AltAddress,
		n.Tags, n.Group, now(), n.ID)
	return err
}

// SetNodeGroup 更新节点分组（批量操作 set-group 使用）。
func (s *Store) SetNodeGroup(id int64, group string) error {
	_, err := s.DB.Exec(`UPDATE nodes SET node_group=?, updated_at=? WHERE id=?`, group, now(), id)
	return err
}

// SetNodeTags 更新节点标签（逗号分隔文本，批量操作 set-tags 使用）。
func (s *Store) SetNodeTags(id int64, tags string) error {
	_, err := s.DB.Exec(`UPDATE nodes SET tags=?, updated_at=? WHERE id=?`, tags, now(), id)
	return err
}

// UpdateNodeInfo 心跳更新主机信息与最近在线时间。
// network_type 仅在传入非空时覆盖，避免心跳路径未载入该字段时被清空。
func (s *Store) UpdateNodeInfo(id int64, n *Node) error {
	_, err := s.DB.Exec(
		`UPDATE nodes SET hostname=?, os_name=?, os_version=?, kernel=?, arch=?,
		   cpu_cores=?, mem_total=?, docker_version=?, agent_version=?, storage_json=?,
		   upgrade_json = CASE WHEN ? <> '' THEN ? ELSE upgrade_json END,
		   last_seen=?,
		   network_type = CASE WHEN ? <> '' THEN ? ELSE network_type END,
		   updated_at=?
		 WHERE id=?`,
		n.Hostname, n.OSName, n.OSVersion, n.Kernel, n.Arch,
		n.CPUCores, n.MemTotal, n.DockerVersion, n.AgentVersion, n.StorageJSON,
		n.UpgradeJSON, n.UpgradeJSON, n.LastSeen,
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
	             docker_mirrors, docker_insecure_registries, agent_version, storage_json, upgrade_json,
	             auto_upgrade, last_seen, owner_user_id, tags, node_group,
	             created_at, updated_at
	      FROM nodes ` + where
	n := &Node{}
	var owner sql.NullInt64
	var autoUpgrade int64
	err := s.DB.QueryRow(q, args...).Scan(
		&n.ID, &n.Name, &n.Mode, &n.Status, &n.NetworkType, &n.Address, &n.AltAddress,
		&n.AgentTokenHash, &n.Hostname, &n.OSName, &n.OSVersion, &n.Kernel, &n.Arch,
		&n.CPUCores, &n.MemTotal, &n.DockerVersion,
		&n.DockerMirrors, &n.DockerInsecureRegistries, &n.AgentVersion, &n.StorageJSON, &n.UpgradeJSON,
		&autoUpgrade, &n.LastSeen, &owner, &n.Tags, &n.Group,
		&n.CreatedAt, &n.UpdatedAt)
	n.AutoUpgrade = autoUpgrade != 0
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	if owner.Valid {
		v := owner.Int64
		n.OwnerUserID = &v
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
		var owner sql.NullInt64
		var autoUpgrade int64
		if err := rows.Scan(
			&n.ID, &n.Name, &n.Mode, &n.Status, &n.NetworkType, &n.Address, &n.AltAddress,
			&n.AgentTokenHash, &n.Hostname, &n.OSName, &n.OSVersion, &n.Kernel, &n.Arch,
			&n.CPUCores, &n.MemTotal, &n.DockerVersion,
			&n.DockerMirrors, &n.DockerInsecureRegistries, &n.AgentVersion, &n.StorageJSON, &n.UpgradeJSON,
			&autoUpgrade, &n.LastSeen, &owner, &n.Tags, &n.Group,
			&n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.AutoUpgrade = autoUpgrade != 0
		if owner.Valid {
			v := owner.Int64
			n.OwnerUserID = &v
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

// SetNodeAutoUpgrade 更新节点「自动升级」开关。
func (s *Store) SetNodeAutoUpgrade(id int64, on bool) error {
	_, err := s.DB.Exec(`UPDATE nodes SET auto_upgrade=?, updated_at=? WHERE id=?`, b2i(on), now(), id)
	return err
}

// GetNodeByToken 按节点长期 Token 明文查询（用于校验 /api/agent-binary 下载请求）。
func (s *Store) GetNodeByToken(plain string) (*Node, error) {
	return s.node("WHERE agent_token_hash = ?", hashToken(plain))
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// hashToken 与 node 服务一致的 Token 哈希算法（sha256 + RawURLEncoding）。
func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

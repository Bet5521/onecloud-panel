package store

// RecordMetricSample 写入一条节点指标采样。
func (s *Store) RecordMetricSample(nodeID, ts int64, cpuPct, memPct, diskPct, load1 float64) error {
	_, err := s.DB.Exec(
		`INSERT INTO metric_samples (node_id, ts, cpu_pct, mem_pct, disk_pct, load1)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		nodeID, ts, cpuPct, memPct, diskPct, load1)
	return err
}

// ListMetricSamples 返回某节点自 since 起的时间序列（升序）。
func (s *Store) ListMetricSamples(nodeID, since int64) ([]MetricSample, error) {
	rows, err := s.DB.Query(
		`SELECT id, node_id, ts, cpu_pct, mem_pct, disk_pct, load1
		 FROM metric_samples WHERE node_id=? AND ts>=? ORDER BY ts ASC`,
		nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricSample
	for rows.Next() {
		var m MetricSample
		if err := rows.Scan(&m.ID, &m.NodeID, &m.Ts, &m.CPUPct, &m.MemPct, &m.DiskPct, &m.Load1); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PurgeMetricSamples 清理早于 before 的采样（维护用）。
func (s *Store) PurgeMetricSamples(before int64) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM metric_samples WHERE ts < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SampleCount 统计当前采样总行数（健康度自检用）。
func (s *Store) SampleCount() (int64, error) {
	var n int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM metric_samples`).Scan(&n)
	return n, err
}

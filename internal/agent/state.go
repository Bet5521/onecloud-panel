package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State Agent 本地持久状态（root-only）。
type State struct {
	Server string `json:"server"`
	NodeID int64  `json:"node_id"`
	Token  string `json:"token"`
}

func statePath(dataDir string) string { return filepath.Join(dataDir, "agent.json") }

func loadState(dataDir string) (*State, error) {
	b, err := os.ReadFile(statePath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveState(dataDir string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(statePath(dataDir), b, 0o600); err != nil {
		return err
	}
	return os.Chmod(statePath(dataDir), 0o600)
}

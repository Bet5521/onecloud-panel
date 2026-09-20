package agent

import "sync"

type tokenHolder struct {
	mu sync.RWMutex
	t  string
}

func (h *tokenHolder) get() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.t
}

func (h *tokenHolder) set(t string) {
	h.mu.Lock()
	h.t = t
	h.mu.Unlock()
}

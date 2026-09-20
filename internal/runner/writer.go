package runner

import (
	"strconv"
	"sync"
	"time"

	"onecloud-panel/internal/store"
)

// taskWriter 任务输出写入器：内存缓冲、定时落库，超 1MiB 保留尾部。
type taskWriter struct {
	store *store.Store
	id    int64

	mu     sync.Mutex
	buf    []byte
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

func newTaskWriter(s *store.Store, id int64) *taskWriter {
	w := &taskWriter{store: s, id: id, stop: make(chan struct{}), done: make(chan struct{})}
	go w.flushLoop()
	return w
}

func (w *taskWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) > maxOutput {
		cut := len(w.buf) - maxOutput
		w.buf = append([]byte("...输出过长，仅保留尾部...\n"), w.buf[cut:]...)
	}
	return len(p), nil
}

// WriteAll 立即把内容并入缓冲并强制落库（用于关键节点）。
func (w *taskWriter) WriteAll(s string) error {
	if _, err := w.Write([]byte(s)); err != nil {
		return err
	}
	return w.flush()
}

func (w *taskWriter) flushLoop() {
	ticker := time.NewTicker(flushEvery)
	defer func() {
		ticker.Stop()
		close(w.done)
	}()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			_ = w.flush()
		}
	}
}

func (w *taskWriter) flush() error {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return nil
	}
	chunk := string(w.buf)
	w.buf = w.buf[:0]
	w.mu.Unlock()
	return w.store.AppendTaskOutput(w.id, chunk)
}

// Close 最后一次落库并停止定时协程。
func (w *taskWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	close(w.stop)
	<-w.done
	return w.flush()
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

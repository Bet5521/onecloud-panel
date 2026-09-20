package executor

import (
	"context"
	"io"
	"os/exec"
)

// Result 命令执行结果。
type Result struct {
	ExitCode int
	Output   string // 合并后的 stdout+stderr
}

// Executor 节点操作执行抽象（本机实现 vs Agent 远程实现）。
type Executor interface {
	// Exec 执行命令并等待结束。
	Exec(ctx context.Context, name string, args ...string) (*Result, error)
	// ExecStream 执行命令并将输出实时写入 w。
	ExecStream(ctx context.Context, w io.Writer, name string, args ...string) (int, error)
	// ReadFile 读取白名单内文件。
	ReadFile(path string) ([]byte, error)
	// WriteFile 写入白名单内文件。
	WriteFile(path string, data []byte) error
	// Exists 判断路径是否存在。
	Exists(path string) (bool, error)
}

// Local 本机执行器。
type Local struct {
	guard *Guard
}

// NewLocal 创建本机执行器；guard 为 nil 表示不限制（仅限内部受信调用）。
func NewLocal(guard *Guard) *Local { return &Local{guard: guard} }

func (l *Local) Exec(ctx context.Context, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	r := &Result{Output: string(out)}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil && r.ExitCode == 0 {
		return r, err
	}
	return r, nil
}

func (l *Local) ExecStream(ctx context.Context, w io.Writer, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		if cmd.ProcessState != nil {
			return cmd.ProcessState.ExitCode(), err
		}
		return -1, err
	}
	return 0, nil
}

func (l *Local) ReadFile(path string) ([]byte, error) {
	if l.guard != nil {
		if err := l.guard.Check(path); err != nil {
			return nil, err
		}
	}
	return readFile(path)
}

func (l *Local) WriteFile(path string, data []byte) error {
	if l.guard != nil {
		if err := l.guard.Check(path); err != nil {
			return err
		}
	}
	return writeFile(path, data)
}

func (l *Local) Exists(path string) (bool, error) {
	return exists(path)
}

package sshx

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// 默认终端参数。
const (
	defaultTerm    = "xterm-256color"
	defaultCols    = 80
	defaultRows    = 24
	maxCols, maxRows = 1024, 512
)

// Shell 交互式 PTY 会话：暴露 stdin/stdout 句柄与窗口尺寸调整。
type Shell struct {
	sess      *ssh.Session
	stdin     io.WriteCloser
	stdout    io.Reader
	closeOnce sync.Once
}

// OpenShell 在远端申请 PTY 并启动默认登录 Shell。
// term 为空时取 xterm-256color；cols/rows 非正值时取默认窗口尺寸。
func (c *Client) OpenShell(term string, cols, rows int) (*Shell, error) {
	if term == "" {
		term = defaultTerm
	}
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("创建远端会话失败: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, fmt.Errorf("打开远端 stdin 失败: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, fmt.Errorf("打开远端 stdout 失败: %w", err)
	}
	// RequestPty 参数顺序为 (term, 高, 宽)
	if err := sess.RequestPty(term, rows, cols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
	}); err != nil {
		sess.Close()
		return nil, fmt.Errorf("申请 PTY 失败: %w", err)
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, fmt.Errorf("启动远端 Shell 失败: %w", err)
	}
	return &Shell{sess: sess, stdin: stdin, stdout: stdout}, nil
}

// Stdin 返回终端输入写入端。
func (s *Shell) Stdin() io.Writer { return s.stdin }

// Stdout 返回终端输出读取端（含 PTY 合并的 stderr）。
func (s *Shell) Stdout() io.Reader { return s.stdout }

// WindowChange 调整远端终端窗口尺寸（自动收敛非法值）。
func (s *Shell) WindowChange(cols, rows int) error {
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	if cols > maxCols {
		cols = maxCols
	}
	if rows > maxRows {
		rows = maxRows
	}
	return s.sess.WindowChange(cols, rows)
}

// Wait 阻塞等待远端 Shell 退出。正常退出返回 nil，非零退出码返回 *ssh.ExitError。
func (s *Shell) Wait() error {
	err := s.sess.Wait()
	if err == nil {
		return nil
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee
	}
	return err
}

// Close 关闭会话（幂等），同时关闭 stdin/stdout。
func (s *Shell) Close() error {
	s.closeOnce.Do(func() {
		_ = s.sess.Close()
	})
	return nil
}

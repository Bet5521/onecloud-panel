// Package sshx 面板侧 SSH 客户端：密码/私钥认证、主机指纹校验与远端命令执行。
//
// 设计约束：
//   - 仅支持密码与私钥认证，不做 agent 转发（面板以 root 运行，转发等于外借面板凭据）；
//   - 默认 pin 策略：首次连接把目标主机指纹回报给调用方，用户确认后再复传比对，
//     绝不使用 ssh.InsecureIgnoreHostKey 静默放行。
package sshx

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// 主机指纹校验策略。
const (
	// PolicyPin 首次连接回报指纹，用户确认后固定比对（默认）。
	PolicyPin = "pin"
	// PolicyStrict 依据 known_hosts 文件严格校验。
	PolicyStrict = "strict"
)

// 默认连接超时与端口。
const (
	defaultPort    = 22
	defaultTimeout = 20 * time.Second
)

// HostKeyError 表示主机指纹尚未确认；包含候选指纹供用户核对。
type HostKeyError struct {
	Host        string
	Fingerprint string
}

func (e *HostKeyError) Error() string {
	return fmt.Sprintf("目标主机 %s 的 SSH 指纹未确认：%s（核对无误后重新提交并填入该指纹）",
		e.Host, e.Fingerprint)
}

// DialConfig SSH 连接参数。
type DialConfig struct {
	Host               string
	User               string
	Password           string
	Port               int
	PrivateKeyPEM      string
	Passphrase         string
	HostKeyPolicy      string
	HostKeyFingerprint string
	KnownHostsFile     string
	Timeout            time.Duration
}

// Client 已建立的 SSH 连接。
type Client struct {
	conn *ssh.Client
}

// Quote 以单引号包裹字符串，用于拼装远端 shell 命令（防注入）。
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// NormalizeFingerprint 归一化指纹文本（去空白）。
func NormalizeFingerprint(s string) string {
	return strings.TrimSpace(s)
}

// Dial 建立 SSH 连接并完成认证。
func Dial(cfg DialConfig, logw io.Writer) (*Client, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, errors.New("SSH 主机地址必填")
	}
	user := strings.TrimSpace(cfg.User)
	if user == "" {
		return nil, errors.New("SSH 用户名必填")
	}
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("SSH 端口非法")
	}
	policy := strings.ToLower(strings.TrimSpace(cfg.HostKeyPolicy))
	if policy == "" {
		policy = PolicyPin
	}
	if policy != PolicyPin && policy != PolicyStrict {
		return nil, errors.New("主机指纹策略仅支持 pin/strict")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	auths, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := hostKeyCallback(policy, cfg, host, logw)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if logw != nil {
		fmt.Fprintf(logw, "[ssh] 正在连接 %s@%s …\n", user, addr)
	}
	conn, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	})
	if err != nil {
		return nil, classifyDialError(err, addr)
	}
	if logw != nil {
		fmt.Fprintf(logw, "[ssh] 已连接 %s@%s\n", user, addr)
	}
	return &Client{conn: conn}, nil
}

// Close 关闭连接。
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Run 在远端执行命令，stdout/stderr 合并写入 w；ctx 取消时发送 SIGTERM。
func (c *Client) Run(ctx context.Context, cmd string, w io.Writer) error {
	session, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("创建远端会话失败: %w", err)
	}
	defer session.Close()
	session.Stdout = w
	session.Stderr = w

	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("启动远端命令失败: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		<-done
		return errors.New("任务已取消，已向远端发送 SIGTERM")
	case err := <-done:
		if err == nil {
			return nil
		}
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("远端命令退出码 %d", ee.ExitStatus())
		}
		return err
	}
}

// authMethods 依据配置构建认证方法。
func authMethods(cfg DialConfig) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod
	if cfg.Password != "" {
		auths = append(auths, ssh.Password(cfg.Password))
		// 兼容 sudo/PAM 风格的键盘交互式认证
		auths = append(auths, ssh.KeyboardInteractive(
			func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = cfg.Password
				}
				return answers, nil
			}))
	}
	if strings.TrimSpace(cfg.PrivateKeyPEM) != "" {
		signer, err := parsePrivateKey(cfg.PrivateKeyPEM, cfg.Passphrase)
		if err != nil {
			return nil, err
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if len(auths) == 0 {
		return nil, errors.New("需提供 SSH 密码或私钥")
	}
	return auths, nil
}

// parsePrivateKey 解析 PEM/OpenSSH 私钥，支持 passphrase。
func parsePrivateKey(pem, passphrase string) (ssh.Signer, error) {
	var (
		signer ssh.Signer
		err    error
	)
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pem), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(pem))
	}
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, errors.New("私钥已加密，请填写私钥口令（passphrase）")
		}
		return nil, errors.New("私钥解析失败：请确认内容为未损坏的 PEM/OpenSSH 私钥")
	}
	return signer, nil
}

// hostKeyCallback 构建主机指纹校验回调。
func hostKeyCallback(policy string, cfg DialConfig, host string, logw io.Writer) (ssh.HostKeyCallback, error) {
	if policy == PolicyStrict {
		if strings.TrimSpace(cfg.KnownHostsFile) == "" {
			return nil, errors.New("strict 策略需要 known_hosts 文件")
		}
		cb, err := knownhosts.New(cfg.KnownHostsFile)
		if err != nil {
			return nil, errors.New("known_hosts 读取失败：" + err.Error())
		}
		return cb, nil
	}
	want := NormalizeFingerprint(cfg.HostKeyFingerprint)
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		if logw != nil {
			fmt.Fprintf(logw, "[ssh] 目标主机 %s 指纹 %s\n", hostname, got)
		}
		if want == "" {
			return &HostKeyError{Host: host, Fingerprint: got}
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
			return fmt.Errorf("主机指纹不匹配：期望 %s，实际 %s（可能遭遇中间人攻击）", want, got)
		}
		return nil
	}, nil
}

// classifyDialError 把底层错误转换为可读原因。
func classifyDialError(err error, addr string) error {
	msg := err.Error()
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("目标主机名解析失败（%s）", addr)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("连接 %s 超时：请确认主机可达且 SSH 端口开放", addr)
	}
	switch {
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "no supported methods remain"),
		strings.Contains(msg, "permission denied"):
		return errors.New("SSH 认证失败：用户名、密码或私钥不正确")
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("目标主机拒绝连接（%s）：SSH 服务未启动或端口不正确", addr)
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"):
		return fmt.Errorf("目标主机不可达（%s）", addr)
	case strings.Contains(msg, "i/o timeout"):
		return fmt.Errorf("连接 %s 超时：请确认主机可达且 SSH 端口开放", addr)
	}
	return fmt.Errorf("SSH 连接失败: %w", err)
}

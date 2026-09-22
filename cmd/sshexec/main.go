// 临时运维工具：用密码 SSH 在节点上执行命令 / 上传文件。
// 仅用于本环境接入实机，验证完成后会从仓库删除。
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func client(host, user, pass string) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	return ssh.Dial("tcp", host, cfg)
}

func runCmd(host, user, pass, cmd string) error {
	c, err := client(host, user, pass)
	if err != nil {
		return err
	}
	defer c.Close()
	s, err := c.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	s.Stdout = os.Stdout
	s.Stderr = os.Stderr
	return s.Run(cmd)
}

func pushFile(host, user, pass, local, remote string) error {
	c, err := client(host, user, pass)
	if err != nil {
		return err
	}
	defer c.Close()
	if i := strings.LastIndex(remote, "/"); i >= 0 {
		sess, err := c.NewSession()
		if err != nil {
			return err
		}
		_ = sess.Run("mkdir -p " + remote[:i])
		sess.Close()
	}
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	if err := sess.Start("scp -t " + remote); err != nil {
		return err
	}
	ack := func() error {
		b := make([]byte, 1)
		if _, err := stdout.Read(b); err != nil {
			return err
		}
		if b[0] != 0 {
			buf := make([]byte, 512)
			n, _ := stdout.Read(buf)
			return fmt.Errorf("scp ack error: %s", string(buf[:n]))
		}
		return nil
	}
	fmt.Fprintf(stdin, "C0644 %d %s\n", len(data), filepath.Base(local))
	if err := ack(); err != nil {
		return err
	}
	if _, err := stdin.Write(data); err != nil {
		return err
	}
	stdin.Write([]byte{0})
	if err := ack(); err != nil {
		return err
	}
	stdin.Write([]byte{0})
	return sess.Wait()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sshexec run|push ...")
		os.Exit(2)
	}
	mode := os.Args[1]
	args := os.Args[2:]
	switch mode {
	case "run":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "run <host:port> <user> <password> <cmd...>")
			os.Exit(2)
		}
		if err := runCmd(args[0], args[1], args[2], strings.Join(args[3:], " ")); err != nil {
			fmt.Fprintln(os.Stderr, "ERR:", err)
			os.Exit(1)
		}
	case "push":
		if len(args) < 5 {
			fmt.Fprintln(os.Stderr, "push <host:port> <user> <password> <local> <remote>")
			os.Exit(2)
		}
		if err := pushFile(args[0], args[1], args[2], args[3], args[4]); err != nil {
			fmt.Fprintln(os.Stderr, "ERR:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown mode:", mode)
		os.Exit(2)
	}
	_ = net.ParseIP
	var _ io.Reader
}

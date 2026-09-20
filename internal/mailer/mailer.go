// Package mailer 通过 SMTP 发送面板通知邮件（密码重置码等）。
package mailer

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
)

// Config SMTP 配置（字段来自面板设置 smtp_*）。
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Enabled 判断配置是否完整可用。
func (c Config) Enabled() bool {
	return c.Host != "" && c.Port != "" && c.From != ""
}

// Send 发送纯文本邮件；465 端口走隐式 TLS，其余端口走 SMTP（支持 STARTTLS）。
func Send(c Config, to, subject, body string) error {
	if !c.Enabled() {
		return fmt.Errorf("SMTP 未完整配置")
	}
	addr := net.JoinHostPort(c.Host, c.Port)
	msg := buildMessage(c.From, to, subject, body)

	if c.Port == "465" {
		return sendImplicitTLS(addr, c, to, msg)
	}
	return sendSTARTTLS(addr, c, to, msg)
}

func sendSTARTTLS(addr string, c Config, to string, msg []byte) error {
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("SMTP 连接失败: %w", err)
	}
	defer client.Close()

	if err := client.Hello(localhost()); err != nil {
		return err
	}
	// 优先 STARTTLS
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
		}
	}
	if c.Username != "" {
		auth := smtp.PlainAuth("", c.Username, c.Password, c.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	return deliver(client, c.From, to, msg)
}

func sendImplicitTLS(addr string, c Config, to string, msg []byte) error {
	tlsConn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: c.Host})
	if err != nil {
		return fmt.Errorf("SMTPS 连接失败: %w", err)
	}
	defer tlsConn.Close()

	client, err := smtp.NewClient(tlsConn, c.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if c.Username != "" {
		auth := smtp.PlainAuth("", c.Username, c.Password, c.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	return deliver(client, c.From, to, msg)
}

func deliver(c *smtp.Client, from, to string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// buildMessage 构造带 UTF-8 主题编码的 RFC 822 邮件。
func buildMessage(from, to, subject, body string) []byte {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: =?UTF-8?B?" +
		base64.StdEncoding.EncodeToString([]byte(subject)) + "?=\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	sb.WriteString(base64.StdEncoding.EncodeToString([]byte(body)))
	return []byte(sb.String())
}

func localhost() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "localhost"
}

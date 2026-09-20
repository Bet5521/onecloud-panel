// Package tlssniff 提供在同一 TCP 端口上同时承载 HTTP 与 HTTPS 的 Listener。
//
// 原理：TLS 握手记录第一个字节恒为 0x16（ContentTypeHandshake），
// Accept 时 Peek 首字节即可区分 TLS 连接与明文 HTTP 连接。
// 这样面板启用 HTTPS 后无需占用第二个端口做跳转。
package tlssniff

import (
	"bufio"
	"crypto/tls"
	"net"
)

// Listener 包装底层 Listener；TLSConfig 为 nil 时不做嗅探（纯 HTTP）。
type Listener struct {
	net.Listener
	TLSConfig *tls.Config
}

// New 创建嗅探 Listener。
func New(ln net.Listener, tlsCfg *tls.Config) *Listener {
	return &Listener{Listener: ln, TLSConfig: tlsCfg}
}

// Accept 接收连接并按首字节判定是否包装为 TLS 连接。
func (l *Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.TLSConfig == nil {
		return c, nil
	}

	pc := &peekConn{Conn: c, r: bufio.NewReaderSize(c, 1)}
	first, err := pc.r.Peek(1)
	if err != nil {
		// 对端连上即断开等：返回带缓冲的连接，交由上层处理/关闭
		_ = c.Close()
		return nil, err
	}
	if first[0] == 0x16 {
		return tls.Server(pc, l.TLSConfig), nil
	}
	return pc, nil
}

// peekConn 使 Peek 缓冲的字节对后续 Read 可见。
type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(p []byte) (int, error) { return c.r.Read(p) }

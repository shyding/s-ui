package network

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
)

// DualHttpHttpsListener transparently multiplexes TLS and plain HTTP on the same port
type DualHttpHttpsListener struct {
	net.Listener
	tlsConfig *tls.Config
}

type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// NewDualHttpHttpsListener wraps an existing net.Listener to support both HTTP and HTTPS
func NewDualHttpHttpsListener(inner net.Listener, tlsConfig *tls.Config) net.Listener {
	return &DualHttpHttpsListener{
		Listener:  inner,
		tlsConfig: tlsConfig,
	}
}

func (l *DualHttpHttpsListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	// Sniff first byte to differentiate TLS Handshake (0x16) vs plain HTTP (G, P, H, etc.)
	buf := make([]byte, 1)
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		conn.Close()
		return nil, err
	}

	bConn := &bufferedConn{
		Conn: conn,
		r:    io.MultiReader(bytes.NewReader(buf), conn),
	}

	// 0x16 is the standard TLS Handshake Record Type
	if buf[0] == 0x16 && l.tlsConfig != nil {
		tlsConn := tls.Server(bConn, l.tlsConfig)
		return tlsConn, nil
	}

	// Plain HTTP connection: returned as raw connection to be handled by http.Server directly
	return bConn, nil
}

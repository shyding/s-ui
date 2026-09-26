package network

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"time"
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
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		// Set a short deadline for sniffing the first byte so idle probes/scanners don't block Accept()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 1)
		_, err = io.ReadFull(conn, buf)
		if err != nil {
			// Probe or scanner closed connection, or timed out.
			// NEVER return this non-temporary error to http.Server or it will kill the entire server!
			conn.Close()
			continue
		}
		// Reset read deadline back to zero for normal HTTP / HTTPS operation
		_ = conn.SetReadDeadline(time.Time{})

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
}

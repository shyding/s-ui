package service

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"
)

func TestTrojanHandshakeUsesProxiedHTTPResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		hashLine, err := reader.ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		sum := sha256.Sum224([]byte("secret"))
		if strings.TrimSpace(hashLine) != hex.EncodeToString(sum[:]) {
			done <- io.ErrUnexpectedEOF
			return
		}
		requestHeader := make([]byte, 10) // CMD + ATYP + IPv4 + port + CRLF
		if _, err := io.ReadFull(reader, requestHeader); err != nil {
			done <- err
			return
		}
		if requestHeader[0] != 1 || requestHeader[1] != 1 || requestHeader[8] != '\r' || requestHeader[9] != '\n' {
			done <- io.ErrUnexpectedEOF
			return
		}
		line, err := reader.ReadString('\n')
		if err != nil || line != "HEAD / HTTP/1.1\r\n" {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
		done <- nil
	}()

	if !trojanHandshake(client, "trojan://secret@dash.icta.top:54150") {
		t.Fatal("expected real HTTP response through Trojan tunnel to pass")
	}
	if err := <-done; err != nil {
		t.Fatalf("server validation failed: %v", err)
	}
}

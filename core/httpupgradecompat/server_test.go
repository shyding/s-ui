package httpupgradecompat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type echoHandler struct{}

func (echoHandler) NewConnectionEx(_ context.Context, c net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
}

func TestUpgradeCompatibility(t *testing.T) {
	s, err := NewServer(context.Background(), log.NewNOPFactory().Logger(), option.V2RayHTTPUpgradeOptions{Host: "example.test", Path: "/upgrade"}, nil, echoHandler{})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go s.Serve(ln)
	for _, tc := range []struct {
		name, path, host, key string
		status                int
	}{
		{"legacy", "/upgrade", "example.test", "", 101},
		{"keyed Xray", "/upgrade", "example.test", "dGhlIHNhbXBsZSBub25jZQ==", 101},
		{"wrong path still rejected", "/wrong", "example.test", "key", 404},
		{"wrong host still rejected", "/upgrade", "wrong.test", "key", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: %s\r\n\r\n", tc.path, tc.host, tc.key)
			r := bufio.NewReader(c)
			response, err := http.ReadResponse(r, nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status %d", response.StatusCode)
			}
			if tc.status != 101 {
				response.Body.Close()
				return
			}
			if tc.key != "" && response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
				t.Fatal("invalid accept key")
			}
			// The stream remains raw VMess bytes, NOT WebSocket framed data.
			payload := "raw-stream-without-websocket-framing"
			_, _ = io.WriteString(c, payload)
			got := make([]byte, len(payload))
			if _, err = io.ReadFull(r, got); err != nil {
				t.Fatal(err)
			}
			if string(got) != payload {
				t.Fatalf("stream altered: %q", got)
			}
		})
	}
}

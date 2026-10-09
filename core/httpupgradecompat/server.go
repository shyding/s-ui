// Package httpupgradecompat accepts both legacy and keyed HTTPUpgrade requests.
// A Sec-WebSocket-Key in Xray HTTPUpgrade is camouflage, not framed WebSocket data.
package httpupgradecompat

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"net"
	"net/http"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/v2rayhttpupgrade"
	"github.com/sagernet/sing/common/logger"
	aTLS "github.com/sagernet/sing/common/tls"
)

type Server struct {
	*v2rayhttpupgrade.Server
	httpServer *http.Server
	tlsConfig  tls.ServerConfig
}

func compatibleHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("Sec-WebSocket-Key"); key != "" {
			// Preserve the original request and all upstream path/host/method checks.
			r = r.Clone(r.Context())
			r.Header.Del("Sec-WebSocket-Key")
			sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			w.Header().Set("Sec-WebSocket-Accept", base64.StdEncoding.EncodeToString(sum[:]))
		}
		next.ServeHTTP(w, r)
	})
}

func NewServer(ctx context.Context, log logger.ContextLogger, options option.V2RayHTTPUpgradeOptions, tlsConfig tls.ServerConfig, handler adapter.V2RayServerTransportHandler) (adapter.V2RayServerTransport, error) {
	upstream, err := v2rayhttpupgrade.NewServer(ctx, log, options, nil, handler)
	if err != nil {
		return nil, err
	}
	return &Server{Server: upstream, tlsConfig: tlsConfig, httpServer: &http.Server{
		Handler: compatibleHandler(upstream), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}}, nil
}

func (s *Server) Serve(listener net.Listener) error {
	if s.tlsConfig != nil {
		s.tlsConfig.SetNextProtos([]string{"http/1.1"})
		listener = aTLS.NewListener(listener, s.tlsConfig)
	}
	return s.httpServer.Serve(listener)
}

func (s *Server) Close() error {
	err := s.httpServer.Close()
	_ = s.Server.Close()
	return err
}

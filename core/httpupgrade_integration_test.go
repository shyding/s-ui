package core

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"golang.org/x/net/proxy"
)

// Opt-in test with a real Xray binary, no system proxy or remote server changes.
func TestVMessHTTPUpgradeXray(t *testing.T) {
	xray := os.Getenv("SUI_TEST_XRAY")
	if xray == "" {
		t.Skip("set SUI_TEST_XRAY to run real-core interoperability test")
	}
	freePort := func() int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "verified-vmess-data") }))
	defer target.Close()
	for _, tlsEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", tlsEnabled), func(t *testing.T) {
			serverPort, socksPort := freePort(), freePort()
			tlsConfig := ""
			security := `"security":"none"`
			if tlsEnabled {
				// Pin the temporary test certificate in this isolated Xray instance;
				// do not weaken verification in generated subscription configs.
				tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
				defer tlsServer.Close()
				cert := tlsServer.TLS.Certificates[0]
				certFile, keyFile := writeTestCertificate(t, cert)
				tlsConfig = fmt.Sprintf(`,"tls":{"enabled":true,"certificate_path":%q,"key_path":%q}`, certFile, keyFile)
				parsed, err := x509.ParseCertificate(cert.Certificate[0])
				if err != nil {
					t.Fatal(err)
				}
				settings, err := json.Marshal(map[string]any{"serverName": parsed.DNSNames[0], "pinnedPeerCertSha256": fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0]))})
				if err != nil {
					t.Fatal(err)
				}
				security = `"security":"tls","tlsSettings":` + string(settings)
			}
			ctx := Context(context.Background(), InboundRegistry(), OutboundRegistry(), EndpointRegistry(), DNSTransportRegistry(), ServiceRegistry())
			var opts option.Options
			raw := fmt.Sprintf(`{"log":{"disabled":true},"inbounds":[{"type":"vmess","tag":"test","listen":"127.0.0.1","listen_port":%d,"users":[{"name":"test","uuid":"11111111-1111-4111-8111-111111111111"}],"transport":{"type":"httpupgrade","path":"/upgrade","host":"example.test"}%s}],"outbounds":[{"type":"direct","tag":"direct"}]}`, serverPort, tlsConfig)
			if err := json.UnmarshalContext(ctx, []byte(raw), &opts); err != nil {
				t.Fatal(err)
			}
			box, err := NewBox(Options{Options: opts, Context: ctx})
			if err != nil {
				t.Fatal(err)
			}
			if err = box.Start(); err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			xconfig := fmt.Sprintf(`{"log":{"loglevel":"warning"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"vmess","settings":{"vnext":[{"address":"127.0.0.1","port":%d,"users":[{"id":"11111111-1111-4111-8111-111111111111","security":"auto"}]}]},"streamSettings":{"network":"httpupgrade",%s,"httpupgradeSettings":{"host":"example.test","path":"/upgrade"}}}]}`, socksPort, serverPort, security)
			path := filepath.Join(t.TempDir(), "xray.json")
			xconfig = strings.Replace(xconfig, `"loglevel":"warning"`, `"loglevel":"debug"`, 1)
			if err = os.WriteFile(path, []byte(xconfig), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(xray, "run", "-c", path)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			addr := fmt.Sprintf("127.0.0.1:%d", socksPort)
			deadline := time.Now().Add(5 * time.Second)
			for {
				c, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
				if e == nil {
					c.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Xray not listening")
				}
				time.Sleep(50 * time.Millisecond)
			}
			dialer, err := proxy.SOCKS5("tcp", addr, nil, &net.Dialer{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{Dial: dialer.Dial}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
			response, err := client.Get(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "verified-vmess-data" {
				t.Fatalf("unexpected response %q", body)
			}
		})
	}
}

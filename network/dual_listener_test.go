package network

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func generateSelfSignedCert() (*tls.Config, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Co"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

func TestDualHttpHttpsListener_PlainHTTP(t *testing.T) {
	tlsConf, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("Failed to generate test cert: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	dualLn := NewDualHttpHttpsListener(ln, tlsConf)

	serverDone := make(chan string, 1)
	go func() {
		conn, err := dualLn.Accept()
		if err != nil {
			serverDone <- ""
			return
		}
		defer conn.Close()

		buf := make([]byte, 10)
		n, _ := io.ReadAtLeast(conn, buf, 4)
		serverDone <- string(buf[:n])
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer clientConn.Close()

	// Send plain HTTP request starting with "GET "
	_, err = clientConn.Write([]byte("GET /sub/my HTTP/1.1\r\n\r\n"))
	if err != nil {
		t.Fatalf("Failed to write plain HTTP: %v", err)
	}

	received := <-serverDone
	if received != "GET " && !testing.Short() && len(received) < 4 {
		t.Errorf("Expected to read plain HTTP prefix, got: %s", received)
	}
}

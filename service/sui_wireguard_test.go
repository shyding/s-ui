package service

import (
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetSUIWireGuardLinksNativeV2RayNFormat(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SUIWireGuardNode{}); err != nil {
		t.Fatal(err)
	}
	opts := json.RawMessage(`{
        "private_key":"private/key+value=",
        "address":["172.16.0.2/32","2606:4700:110:8abc::2/128"],
        "peers":[{"address":"engage.cloudflareclient.com","port":2408,"public_key":"public/key+value=","reserved":[1,2,3]}]
    }`)
	for i, port := range []int{54181, 54182} {
		if err := db.Create(&model.SUIWireGuardNode{Tag: "wg-" + string(rune('a'+i)), Port: port, Options: opts}).Error; err != nil {
			t.Fatal(err)
		}
	}
	links := GetSUIWireGuardLinks(db)
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	for i, raw := range links {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if u.Scheme != "wireguard" || u.Hostname() != SUIServerName || u.Port() != []string{"54181", "54182"}[i] {
			t.Fatalf("unexpected endpoint in %q", raw)
		}
		if u.User.Username() != "private/key+value=" || u.Query().Get("publickey") != "public/key+value=" {
			t.Fatalf("keys did not round-trip through URI encoding: %q", raw)
		}
		if !strings.Contains(u.Query().Get("address"), "172.16.0.2/32") || u.Query().Get("reserved") != "1,2,3" {
			t.Fatalf("missing native WireGuard parameters: %q", raw)
		}
	}
}

func TestEnsureSUIWireGuardNodesMigratesOccupiedLegacyPort(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SUIWireGuardNode{}); err != nil {
		t.Fatal(err)
	}
	opts := json.RawMessage(`{
        "private_key":"private/key+value=",
        "address":["172.16.0.2/32"],
        "peers":[{"address":"engage.cloudflareclient.com","port":2408,"public_key":"public/key+value=","reserved":[1,2,3]}]
    }`)
	for i, port := range []int{54180, 54181} {
		if err := db.Create(&model.SUIWireGuardNode{Tag: "wireguard-warp-" + []string{"54180", "54181"}[i], Port: port, Options: opts}).Error; err != nil {
			t.Fatal(err)
		}
	}
	created, err := EnsureSUIWireGuardNodes(db)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("migration must preserve both identities, created %d", created)
	}
	var ports []int
	if err := db.Model(&model.SUIWireGuardNode{}).Order("port ASC").Pluck("port", &ports).Error; err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0] != 54181 || ports[1] != 54182 {
		t.Fatalf("unexpected migrated ports: %v", ports)
	}
}

func TestSUIWireGuardUDPRelayRoundTrip(t *testing.T) {
	backend, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		buffer := make([]byte, 2048)
		n, sender, readErr := backend.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = backend.WriteToUDP(append([]byte("reply:"), buffer[:n]...), sender)
		}
	}()

	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go runSUIWireGuardUDPRelay(listener, backend.LocalAddr().(*net.UDPAddr))

	client, err := net.DialUDP("udp4", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("handshake")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	n, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "reply:handshake" {
		t.Fatalf("unexpected relay response %q", got)
	}
}

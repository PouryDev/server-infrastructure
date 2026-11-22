//go:build vpnintegration

package vpn

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// Exercises the actual installed core, a local VMess server and local HTTP target;
// no real VPN credentials or internet connectivity are required.
func TestRealXrayLifecycle(t *testing.T) {
	binary := os.Getenv("MANAGER_TEST_XRAY")
	if binary == "" {
		t.Fatal("MANAGER_TEST_XRAY is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := object{"log": object{"loglevel": "none"}, "inbounds": []any{object{"listen": "127.0.0.1", "port": serverPort, "protocol": "vmess", "settings": object{"clients": []any{object{"id": testID}}}}}, "outbounds": []any{object{"protocol": "freedom", "settings": object{"finalRules": []any{object{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}}}}}}}
	b, _ := json.Marshal(config)
	path := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(path, b, 0600)
	server := exec.Command(binary, "run", "-config", path)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "through-vmess") }))
	defer target.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	engine, err := Open(ctx, binary, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { engine.mu.Lock(); defer engine.mu.Unlock(); engine.stopLocked() }()
	status, _ := engine.Status(ctx)
	if status.Configured || status.Running {
		t.Fatal(status)
	}
	data, _ := json.Marshal(object{"add": "127.0.0.1", "port": strconv.Itoa(serverPort), "id": testID, "aid": "0", "net": "tcp", "tls": ""})
	link := "vmess://" + base64.StdEncoding.EncodeToString(data)
	status, err = engine.Change(ctx, domain.VPNChange{Link: link, Enabled: true, Revision: status.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Fatal(status)
	}
	proxy, _ := url.Parse("http://127.0.0.1:10809")
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	assertTraffic := func() {
		t.Helper()
		res, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if string(body) != "through-vmess" {
			t.Fatal(res.StatusCode, string(body))
		}
	}
	assertTraffic()
	before := status
	// A persistence failure must restore the old listening process and state.
	engine.dir = filepath.Join(dir, "missing")
	if _, err := engine.Change(ctx, domain.VPNChange{Enabled: false, Revision: status.Revision}); err == nil {
		t.Fatal("missing storage was accepted")
	}
	engine.dir = dir
	status, _ = engine.Status(ctx)
	if status != before {
		t.Fatal("storage failure changed saved state")
	}
	assertTraffic()
	if _, err := engine.Change(ctx, domain.VPNChange{Link: "vless://" + testID + "@vpn.example:443?security=reality&sni=example.com&pbk=invalid", Enabled: true, Revision: status.Revision}); err == nil {
		t.Fatal("core accepted malformed REALITY key")
	}

	if _, err := engine.Change(ctx, domain.VPNChange{Link: "vless://bad@vpn.example:443?type=invalid", Enabled: true, Revision: status.Revision}); err == nil {
		t.Fatal("invalid config accepted")
	}
	status, _ = engine.Status(ctx)
	if status != before {
		t.Fatal("invalid change replaced previous state")
	}
	assertTraffic()
	if _, err := engine.Change(ctx, domain.VPNChange{Enabled: false, Revision: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("stale write accepted")
	}
	status, err = engine.Change(ctx, domain.VPNChange{Enabled: false, Revision: status.Revision})
	if err != nil || status.Running || !status.Configured {
		t.Fatal(status, err)
	}
	if _, err := client.Get(target.URL); err == nil {
		t.Fatal("disabled proxy still accepts traffic")
	}
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	// Restart restores the saved disabled state and keeps the imported link.
	reopened, err := Open(ctx, binary, dir)
	if err != nil {
		t.Fatal(err)
	}
	engine = reopened
	status, _ = engine.Status(ctx)
	status, err = engine.Change(ctx, domain.VPNChange{Enabled: true, Revision: status.Revision})
	if err != nil {
		t.Fatal(err)
	}
	assertTraffic()
	// A process restart restores the enabled state, too.
	engine.mu.Lock()
	engine.stopLocked()
	engine.mu.Unlock()
	reopened, err = Open(ctx, binary, dir)
	if err != nil {
		t.Fatal(err)
	}
	engine = reopened
	assertTraffic()
	// Supervisor recovers from an unexpected core exit.
	done := make(chan struct{})
	go func() { defer close(done); engine.Run(ctx) }()
	engine.mu.Lock()
	engine.cmd.Process.Kill()
	<-engine.done
	engine.mu.Unlock()
	deadline := time.Now().Add(8 * time.Second)
	for {
		status, _ = engine.Status(ctx)
		if status.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not restart core")
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertTraffic()
	cancel()
	<-done
}

func TestRealCoreAcceptsSupportedTransports(t *testing.T) {
	binary := os.Getenv("MANAGER_TEST_XRAY")
	if binary == "" {
		t.Fatal("MANAGER_TEST_XRAY is required")
	}
	engine, err := Open(context.Background(), binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	for _, transport := range []string{"tcp", "ws", "grpc", "xhttp", "httpupgrade"} {
		link := "vless://" + testID + "@vpn.example:443?security=tls&type=" + transport + "&sni=vpn.example&path=%2Fvpn&serviceName=vpn"
		b, _, _, err := parseLink(link)
		if err != nil {
			t.Fatal(transport, err)
		}
		if err := engine.validate(context.Background(), b); err != nil {
			t.Fatal(transport, err)
		}
	}
	for _, link := range []string{
		"vless://" + testID + "@vpn.example:443?security=reality&type=tcp&sni=example.com&pbk=" + public + "&sid=ab12&flow=xtls-rprx-vision",
		"trojan://secret@vpn.example:443?sni=vpn.example",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret")) + "@vpn.example:443",
	} {
		b, _, _, err := parseLink(link)
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.validate(context.Background(), b); err != nil {
			t.Fatal(strings.SplitN(link, ":", 2)[0], err)
		}
	}
}

package vpn

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const testID = "92f91d9d-1e37-4b4e-acf3-2e00ef92a609"

func TestShareLinks(t *testing.T) {
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","add":"vpn.example","port":"443","id":"` + testID + `","aid":"0","net":"ws","type":"none","host":"front.example","path":"/vpn","tls":"tls","sni":"vpn.example"}`))
	ss := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret:with-colon"))
	for name, link := range map[string]string{
		"vless":  "vless://" + testID + "@vpn.example:443?security=reality&type=tcp&sni=example.com&fp=chrome&pbk=public-key&sid=ab12&flow=xtls-rprx-vision#private-label",
		"vmess":  "vmess://" + vmess,
		"trojan": "trojan://secret@vpn.example:443?type=grpc&serviceName=tunnel&sni=vpn.example",
		"ss":     "ss://" + ss + "@[2001:db8::1]:443",
	} {
		t.Run(name, func(t *testing.T) {
			b, protocol, server, err := parseLink(link)
			if err != nil || protocol != name || server == "" {
				t.Fatal(protocol, server, err)
			}
			var config struct {
				Inbounds  []object
				Outbounds []object
			}
			if json.Unmarshal(b, &config) != nil {
				t.Fatal("invalid config")
			}
			wantProtocol := name
			if name == "ss" {
				wantProtocol = "shadowsocks"
			}
			if len(config.Inbounds) != 2 || len(config.Outbounds) != 1 || config.Outbounds[0]["protocol"] != wantProtocol {
				t.Fatal("incorrect proxy configuration")
			}
			if strings.Contains(string(b), "private-label") || strings.Contains(string(b), "freedom") {
				t.Fatal("metadata leak or direct fallback")
			}
			if name == "vless" && !strings.Contains(string(b), `"password":"public-key"`) {
				t.Fatal("REALITY client password is missing")
			}
		})
	}
}

func TestInvalidAndUnsupportedLinks(t *testing.T) {
	base := "vless://" + testID + "@vpn.example:443?"
	for _, link := range []string{
		"", "https://subscription.example/key", "vmess://broken", "vless://secret@vpn.example:0", "vless://vpn.example:443", base + "allowInsecure=1", base + "type=kcp", base + "security=xtls", base + "type=ws&extra=secret", base + "security=reality&sni=example.com", base + "type=tcp&type=ws", base + "headerType=http", base + "encryption=unknown", "ss://bad@vpn.example:443?plugin=obfs", strings.Repeat("x", 16385),
	} {
		if _, _, _, err := parseLink(link); err == nil {
			t.Errorf("accepted unsupported or invalid link (length=%d)", len(link))
		}
	}
}

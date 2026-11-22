package vpn

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type object = map[string]any

func unbase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("Invalid VPN link")
}

// Parse only explicit share links; subscriptions/remote config URLs are never fetched.
func parseLink(link string) (json.RawMessage, string, string, error) {
	link = strings.TrimSpace(link)
	if len(link) == 0 || len(link) > 16384 || strings.ContainsAny(link, "\r\n\t ") {
		return nil, "", "", fmt.Errorf("Invalid VPN link")
	}
	u, err := url.Parse(link)
	if err != nil {
		return nil, "", "", fmt.Errorf("Invalid VPN link")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, "", "", fmt.Errorf("Invalid VPN link")
	}
	protocol := u.Scheme
	var settings object
	if protocol == "vmess" {
		b, err := unbase64(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			return nil, "", "", err
		}
		var v map[string]json.RawMessage
		if json.Unmarshal(b, &v) != nil {
			return nil, "", "", fmt.Errorf("Invalid VPN link")
		}
		field := func(key string) string {
			var s string
			if json.Unmarshal(v[key], &s) == nil {
				return s
			}
			return string(v[key])
		}
		if aid := field("aid"); aid != "" && aid != "0" {
			return nil, "", "", fmt.Errorf("Unsupported VPN link options")
		}
		u = &url.URL{Scheme: protocol, Host: net.JoinHostPort(field("add"), field("port")), User: url.User(field("id"))}
		for _, pair := range [][2]string{{"type", "net"}, {"security", "tls"}, {"sni", "sni"}, {"host", "host"}, {"path", "path"}, {"fp", "fp"}, {"alpn", "alpn"}} {
			if val := field(pair[1]); val != "" {
				q.Set(pair[0], val)
			}
		}
		if field("type") != "" && field("type") != "none" {
			return nil, "", "", fmt.Errorf("Unsupported VPN link options")
		}
		cipher := field("scy")
		if cipher == "" {
			cipher = "auto"
		}
		switch cipher {
		case "auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero":
		default:
			return nil, "", "", fmt.Errorf("Unsupported VPN link options")
		}
		settings = object{"vnext": []any{object{"address": u.Hostname(), "port": port(u.Port()), "users": []any{object{"id": u.User.Username(), "security": cipher, "alterId": 0}}}}}
	} else if protocol == "ss" {
		// SIP002: base64(method:password)@host:port; legacy whole-link base64 too.
		if u.User == nil {
			raw := strings.SplitN(strings.TrimPrefix(link, "ss://"), "#", 2)[0]
			b, err := unbase64(raw)
			if err != nil {
				return nil, "", "", err
			}
			u, err = url.Parse("ss://" + string(b))
			if err != nil {
				return nil, "", "", fmt.Errorf("Invalid VPN link")
			}
		}
		if u.User == nil {
			return nil, "", "", fmt.Errorf("Invalid VPN link")
		}
		method, password := u.User.Username(), ""
		if p, ok := u.User.Password(); ok {
			password = p
		} else {
			b, err := unbase64(method)
			if err != nil {
				return nil, "", "", err
			}
			parts := strings.SplitN(string(b), ":", 2)
			if len(parts) != 2 {
				return nil, "", "", fmt.Errorf("Invalid VPN link")
			}
			method, password = parts[0], parts[1]
		}
		if password == "" {
			return nil, "", "", fmt.Errorf("Invalid VPN link")
		}
		settings = object{"servers": []any{object{"address": u.Hostname(), "port": port(u.Port()), "method": method, "password": password}}}
	} else if protocol == "vless" || protocol == "trojan" {
		if u.User == nil || u.User.Username() == "" {
			return nil, "", "", fmt.Errorf("Invalid VPN link")
		}
		if _, ok := u.User.Password(); ok {
			return nil, "", "", fmt.Errorf("Invalid VPN link")
		}
		if protocol == "vless" {
			encryption := q.Get("encryption")
			if encryption == "" {
				encryption = "none"
			}
			if encryption != "none" {
				return nil, "", "", fmt.Errorf("Unsupported VPN link options")
			}
			settings = object{"vnext": []any{object{"address": u.Hostname(), "port": port(u.Port()), "users": []any{object{"id": u.User.Username(), "encryption": encryption, "flow": q.Get("flow")}}}}}
		} else {
			settings = object{"servers": []any{object{"address": u.Hostname(), "port": port(u.Port()), "password": u.User.Username()}}}
			if q.Get("security") == "" {
				q.Set("security", "tls")
			}
		}
	} else {
		return nil, "", "", fmt.Errorf("Unsupported VPN protocol")
	}
	if u.Hostname() == "" || port(u.Port()) == 0 || (protocol != "ss" && u.Path != "" && u.Path != "/") {
		return nil, "", "", fmt.Errorf("Invalid VPN link")
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("type security encryption flow sni serverName fp pbk sid spx alpn host path serviceName mode authority headerType allowInsecure insecure") {
		allowed[key] = true
	}
	for key, vals := range q {
		if !allowed[key] || len(vals) != 1 {
			return nil, "", "", fmt.Errorf("Unsupported VPN link options")
		}
	}
	for _, key := range []string{"allowInsecure", "insecure"} {
		if val := q.Get(key); val != "" && val != "0" && val != "false" {
			return nil, "", "", fmt.Errorf("Insecure VPN links are not allowed")
		}
	}
	if q.Get("headerType") != "" && q.Get("headerType") != "none" {
		return nil, "", "", fmt.Errorf("Unsupported VPN link options")
	}
	if flow := q.Get("flow"); flow != "" && flow != "xtls-rprx-vision" {
		return nil, "", "", fmt.Errorf("Unsupported VPN link options")
	}
	stream, err := streamSettings(q)
	if err != nil {
		return nil, "", "", err
	}
	if protocol == "ss" && len(q) != 0 {
		return nil, "", "", fmt.Errorf("Unsupported VPN link options")
	}
	outboundProtocol := protocol
	if protocol == "ss" {
		outboundProtocol = "shadowsocks"
	}
	config := object{
		"log": object{"loglevel": "none"},
		"inbounds": []any{
			object{"tag": "socks", "listen": "0.0.0.0", "port": 10808, "protocol": "socks", "settings": object{"auth": "noauth", "udp": true}},
			object{"tag": "http", "listen": "0.0.0.0", "port": 10809, "protocol": "http", "settings": object{}},
		},
		"outbounds": []any{object{"tag": "vpn", "protocol": outboundProtocol, "settings": settings, "streamSettings": stream}},
	}
	b, err := json.Marshal(config)
	return b, protocol, u.Host, err
}

func port(value string) int {
	p, _ := strconv.Atoi(value)
	if p < 1 || p > 65535 {
		return 0
	}
	return p
}

func streamSettings(q url.Values) (object, error) {
	network := q.Get("type")
	if network == "" || network == "tcp" {
		network = "raw"
	}
	stream := object{"network": network}
	switch network {
	case "raw":
	case "ws":
		stream["wsSettings"] = object{"path": q.Get("path"), "headers": object{"Host": q.Get("host")}}
	case "grpc":
		if q.Get("mode") != "" && q.Get("mode") != "gun" && q.Get("mode") != "multi" {
			return nil, fmt.Errorf("Unsupported VPN link options")
		}
		stream["grpcSettings"] = object{"serviceName": q.Get("serviceName"), "authority": q.Get("authority"), "multiMode": q.Get("mode") == "multi"}
	case "xhttp":
		mode := q.Get("mode")
		if mode == "" {
			mode = "auto"
		}
		stream["xhttpSettings"] = object{"path": q.Get("path"), "host": q.Get("host"), "mode": mode}
	case "httpupgrade":
		stream["httpupgradeSettings"] = object{"path": q.Get("path"), "host": q.Get("host")}
	default:
		return nil, fmt.Errorf("Unsupported VPN transport")
	}
	security := q.Get("security")
	if security == "" {
		security = "none"
	}
	stream["security"] = security
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("serverName")
	}
	fingerprint := q.Get("fp")
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	switch security {
	case "none":
	case "tls":
		tls := object{"serverName": sni, "fingerprint": fingerprint}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		stream["tlsSettings"] = tls
	case "reality":
		if q.Get("pbk") == "" || sni == "" || (network != "raw" && network != "xhttp" && network != "grpc") {
			return nil, fmt.Errorf("Invalid REALITY settings")
		}
		stream["realitySettings"] = object{"serverName": sni, "fingerprint": fingerprint, "password": q.Get("pbk"), "shortId": q.Get("sid"), "spiderX": q.Get("spx")}
	default:
		return nil, fmt.Errorf("Unsupported VPN security")
	}
	return stream, nil
}

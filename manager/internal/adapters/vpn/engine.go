package vpn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type savedState struct {
	Config   json.RawMessage `json:"config"`
	Enabled  bool            `json:"enabled"`
	Protocol string          `json:"protocol"`
	Server   string          `json:"server"`
}

// Engine owns only its child process and private state directory. No Docker socket.
type Engine struct {
	mu          sync.Mutex
	binary, dir string
	state       savedState
	cmd         *exec.Cmd
	done        chan struct{}
}

func Open(ctx context.Context, binary, dir string) (*Engine, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("VPN storage unavailable")
	}
	e := &Engine{binary: binary, dir: dir}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		if json.Unmarshal(b, &e.state) != nil || (e.state.Enabled && len(e.state.Config) == 0) {
			return nil, fmt.Errorf("VPN state is invalid")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("VPN storage unavailable")
	}
	if e.state.Enabled {
		if err := e.startLocked(ctx, e.state); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func (e *Engine) runningLocked() bool {
	if e.done == nil {
		return false
	}
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}
func (e *Engine) statusLocked() domain.VPNStatus {
	b, _ := json.Marshal(e.state)
	sum := sha256.Sum256(b)
	return domain.VPNStatus{Configured: len(e.state.Config) > 0, Enabled: e.state.Enabled, Running: e.runningLocked(), Protocol: e.state.Protocol, Server: e.state.Server, Revision: hex.EncodeToString(sum[:])}
}
func (e *Engine) Status(context.Context) (domain.VPNStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(), nil
}

func (e *Engine) configFile(config json.RawMessage) (string, error) {
	f, err := os.CreateTemp("", "xray-*.json")
	if err != nil {
		return "", fmt.Errorf("VPN storage unavailable")
	}
	name := f.Name()
	if _, err = f.Write(config); err != nil {
		f.Close()
		os.Remove(name)
		return "", fmt.Errorf("VPN storage unavailable")
	}
	if err = f.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("VPN storage unavailable")
	}
	return name, nil
}

func (e *Engine) validate(ctx context.Context, config json.RawMessage) error {
	name, err := e.configFile(config)
	if err != nil {
		return err
	}
	defer os.Remove(name)
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Diagnostics can contain secrets. Never attach stdout/stderr to any response/log.
	if exec.CommandContext(check, e.binary, "run", "-test", "-config", name).Run() != nil {
		return fmt.Errorf("Xray rejected VPN configuration")
	}
	return nil
}

func (e *Engine) stopLocked() {
	if e.cmd != nil && e.runningLocked() {
		_ = e.cmd.Process.Kill()
		<-e.done
	}
	e.cmd = nil
	e.done = nil
}

func (e *Engine) startLocked(ctx context.Context, state savedState) error {
	if !state.Enabled {
		return nil
	}
	name, err := e.configFile(state.Config)
	if err != nil {
		return err
	}
	cmd := exec.Command(e.binary, "run", "-config", name)
	if cmd.Start() != nil {
		os.Remove(name)
		return fmt.Errorf("Xray could not start")
	}
	e.cmd = cmd
	e.done = make(chan struct{})
	done := e.done
	go func() { _ = cmd.Wait(); os.Remove(name); close(done) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return fmt.Errorf("Xray could not start")
		case <-ctx.Done():
			e.stopLocked()
			return fmt.Errorf("Xray could not start")
		case <-deadline.C:
			e.stopLocked()
			return fmt.Errorf("Xray could not start")
		case <-tick.C:
			ready := true
			for _, address := range []string{"127.0.0.1:10808", "127.0.0.1:10809"} {
				conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
				if err != nil {
					ready = false
				} else {
					conn.Close()
				}
			}
			if ready && e.runningLocked() {
				return nil
			}
		}
	}
}

func (e *Engine) persist(state savedState) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(e.dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(e.dir, "state.json")); err != nil {
		return err
	}
	return nil
}

func (e *Engine) Change(ctx context.Context, in domain.VPNChange) (domain.VPNStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if in.Revision != e.statusLocked().Revision {
		return domain.VPNStatus{}, fmt.Errorf("VPN settings changed; refresh before saving")
	}
	next := e.state
	next.Enabled = in.Enabled
	if in.Link != "" {
		var err error
		next.Config, next.Protocol, next.Server, err = parseLink(in.Link)
		if err != nil {
			return domain.VPNStatus{}, err
		}
	}
	if len(next.Config) == 0 {
		return domain.VPNStatus{}, fmt.Errorf("Enter a VPN link first")
	}
	if err := e.validate(ctx, next.Config); err != nil {
		return domain.VPNStatus{}, err
	}
	previous := e.state
	e.stopLocked()
	err := e.startLocked(ctx, next)
	if err == nil {
		if e.persist(next) != nil {
			err = fmt.Errorf("VPN storage unavailable")
		}
	}
	if err != nil {
		e.stopLocked()
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if restoreErr := e.startLocked(rollback, previous); restoreErr != nil {
			return domain.VPNStatus{}, fmt.Errorf("VPN change failed; previous connection could not restart")
		}
		return domain.VPNStatus{}, err
	}
	e.state = next
	return e.statusLocked(), nil
}

func (e *Engine) Test(ctx context.Context) error {
	e.mu.Lock()
	running := e.state.Enabled && e.runningLocked()
	e.mu.Unlock()
	if !running {
		return fmt.Errorf("Enable VPN before testing")
	}
	proxy, _ := url.Parse("http://127.0.0.1:10809")
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.gstatic.com/generate_204", nil)
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("VPN connectivity test failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("VPN connectivity test failed")
	}
	return nil
}

// Run restarts a crashed child with bounded backoff. Container shutdown reaps it.
func (e *Engine) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	defer func() { e.mu.Lock(); defer e.mu.Unlock(); e.stopLocked() }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.mu.Lock()
			if e.state.Enabled && !e.runningLocked() {
				_ = e.startLocked(ctx, e.state)
			}
			e.mu.Unlock()
		}
	}
}

package delivery

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func remoteCommand(args []string) string {
	var words []string
	for _, arg := range args {
		words = append(words, quote(arg))
	}
	return strings.Join(words, " ")
}

func (e *Engine) destination(ctx context.Context, t domain.ShipTarget, args ...string) (*exec.Cmd, error) {
	if err := application.ValidateTarget(t); err != nil {
		return nil, err
	}
	if t.Mode == "local" {
		return command(ctx, "", args...), nil
	}
	identity := filepath.Join(e.KeysRoot, t.Identity)
	if info, err := os.Lstat(identity); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SSH identity is unavailable")
	}
	if _, err := os.Stat(filepath.Join(e.KeysRoot, "known_hosts")); err != nil {
		return nil, fmt.Errorf("trusted SSH known_hosts file is missing")
	}
	sshArgs := []string{"ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + filepath.Join(e.KeysRoot, "known_hosts"), "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-i", identity, "-p", strconv.Itoa(t.Port), t.User + "@" + t.Host, remoteCommand(args)}
	return command(ctx, "", sshArgs...), nil
}
func (e *Engine) remote(ctx context.Context, t domain.ShipTarget, stdin io.Reader, args ...string) ([]byte, error) {
	cmd, err := e.destination(ctx, t, args...)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = stdin
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("destination command failed; check connectivity, host key, permissions and Docker prerequisites")
	}
	return output.Bytes(), nil
}
func (e *Engine) Probe(ctx context.Context, t domain.ShipTarget) (domain.ShipProbe, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	data, err := e.remote(ctx, t, nil, "sh", "-c", `set -e; command -v tar >/dev/null; command -v flock >/dev/null; docker info --format '{{.OSType}}/{{.Architecture}}'; docker version --format '{{.Server.Version}}'; docker compose version --short`)
	if err != nil {
		return domain.ShipProbe{}, err
	}
	parts := strings.Fields(string(data))
	if len(parts) != 3 {
		return domain.ShipProbe{}, fmt.Errorf("unexpected destination probe response")
	}
	platform := strings.ReplaceAll(strings.ReplaceAll(parts[0], "x86_64", "amd64"), "aarch64", "arm64")
	return domain.ShipProbe{Platform: platform, Docker: parts[1], Compose: parts[2]}, nil
}

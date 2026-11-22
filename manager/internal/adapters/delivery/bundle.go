package delivery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func relativeTo(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	return rel, err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
func (e *Engine) addFiles(root, rel string, b *bundle) error {
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("configuration path escapes project root")
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range b.Files {
		total += len(f.Data)
		seen[f.Path] = true
	}
	return filepath.WalkDir(filepath.Join(root, rel), func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("configuration file is missing or unreadable")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinked configuration files are not shipped")
		}
		if entry.IsDir() {
			return nil
		}
		if (entry.Name() == ".env" || strings.HasPrefix(entry.Name(), ".env.")) && !b.AllowEnv {
			return fmt.Errorf("configuration contains an env file; explicitly enable env-file shipping")
		}
		name, ok := relativeTo(root, p)
		if !ok || strings.HasPrefix(name, "manager/ssh/") || strings.Contains(name, "/node_modules/") || strings.Contains(name, "/.git/") {
			return fmt.Errorf("configuration path is not allowed")
		}
		if seen[name] {
			return nil
		}
		if len(b.Files) >= 10000 {
			return fmt.Errorf("too many configuration files")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxBundle || total+int(info.Size()) > maxBundle {
			return fmt.Errorf("configuration bundle exceeds file limits")
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("configuration file unreadable")
		}
		total += len(data)
		seen[name] = true
		mode := int64(info.Mode().Perm())
		if entry.Name() == ".env" || strings.HasPrefix(entry.Name(), ".env.") {
			mode = 0600
		}
		b.Files = append(b.Files, bundleFile{Path: filepath.ToSlash(name), Data: data, Mode: mode})
		return nil
	})
}

func (e *Engine) bindings(source, destination string, t domain.ShipTarget, svc, resolved map[string]any, includeEnv bool, b *bundle) error {
	for i, value := range list(svc["volumes"]) {
		v := object(value)
		if text(v["type"]) != "bind" {
			continue
		}
		local := text(v["source"])
		if i < len(list(resolved["volumes"])) {
			local = text(object(list(resolved["volumes"])[i])["source"])
		}
		if !filepath.IsAbs(local) {
			local = filepath.Join(source, local)
		}
		if rel, ok := relativeTo(source, local); ok {
			if rel == "manager/ssh" || strings.HasPrefix(rel, "manager/ssh/") {
				v["source"] = filepath.Join(destination, rel)
				continue
			}
			if rel == "." {
				v["source"] = strings.TrimSuffix(releasePrefix, "/")
				continue
			}
			if filepath.Base(local) == ".env" && !includeEnv {
				v["source"] = filepath.Join(destination, rel)
				continue
			}
			if err := e.addFiles(source, rel, b); err != nil {
				return err
			}
			v["source"] = releasePrefix + filepath.ToSlash(rel)
			continue
		}
		mapped := false
		for _, pair := range [][2]string{{e.HostDataRoot, t.DataDir}, {"/srv/data", t.DataDir}, {e.HostAppsRoot, t.AppsDir}, {e.HostInfraRoot, t.InfraDir}} {
			if pair[0] == "" {
				continue
			}
			if rel, ok := relativeTo(pair[0], local); ok {
				v["source"] = filepath.Join(pair[1], rel)
				mapped = true
				break
			}
		}
		if mapped {
			continue
		}
		// System instrumentation mounts retain their host meaning for infra only.
		allowed := local == "/tmp/infra-manager-target" || local == "/var/run/docker.sock" || local == "/" || strings.HasPrefix(local, "/proc") || strings.HasPrefix(local, "/sys") || strings.HasPrefix(local, "/dev") || strings.HasPrefix(local, "/var/lib/docker") || strings.HasPrefix(local, "/var/run")
		if allowed && destination == t.InfraDir {
			v["source"] = local
			continue
		}
		return fmt.Errorf("bind mount is outside configured source roots: %s", local)
	}
	for _, value := range list(svc["env_file"]) {
		env := object(value)
		file := text(env["path"])
		if file == "" {
			return fmt.Errorf("unsupported env file")
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(source, file)
		}
		rel, ok := relativeTo(source, file)
		if !ok {
			return fmt.Errorf("env file escapes project root")
		}
		if includeEnv {
			if err := e.addFiles(source, rel, b); err != nil {
				return err
			}
			env["path"] = releasePrefix + filepath.ToSlash(rel)
		} else {
			env["path"] = filepath.Join(destination, rel)
		}
	}
	return nil
}

func replaceRelease(value any, dir string) any {
	switch v := value.(type) {
	case string:
		if v == strings.TrimSuffix(releasePrefix, "/") {
			return dir
		}
		if strings.HasPrefix(v, releasePrefix) {
			return filepath.Join(dir, strings.TrimPrefix(v, releasePrefix))
		}
		return v
	case []any:
		for i := range v {
			v[i] = replaceRelease(v[i], dir)
		}
		return v
	case map[string]any:
		for k := range v {
			v[k] = replaceRelease(v[k], dir)
		}
		return v
	default:
		return value
	}
}

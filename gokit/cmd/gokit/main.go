// Command gokit scaffolds new projects that use this kit and prints keys.
//
//	gokit new <dir> --module github.com/you/shop [--kit-src /kit] [--go-image IMAGE]
//	gokit sync <dir> --kit-src /kit
//	gokit jwt-key [--hs256]
//
// It never runs on the host: the repository Makefile starts it inside the Go container.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/auth"
)

const usage = `usage:
  gokit new <dir> --module <go module path> [options]   create a clean-architecture project
  gokit sync <dir> --kit-src <path>                     refresh the vendored kit copy (.kit) of a project
  gokit jwt-key [--hs256]                               print a new Ed25519 key pair or an HS256 secret

options for "new":
  --name        project name (default: the last element of <dir>); same rule as "make new-project"
  --module      Go module path of the new project (default github.com/PouryDev/<name>)
  --kit-mode    local  copy the kit into <dir>/.kit and "replace" it (default, works before the kit is published)
                remote require the kit at --kit-version from its repository
  --kit-src     directory of the kit source (needed for local mode)
  --kit-module  import path of the kit (default: read from --kit-src/go.mod)
  --kit-version version for remote mode (default v0.1.0)
  --go-image    Go builder image (default ` + defaultGoImage + `)
  --db-image    PostgreSQL image for local development (default ` + defaultDBImage + `)
  --lint-image  golangci-lint image (default ` + defaultLintImage + `)
  --redis-image Redis image for local development (default ` + defaultRedisImage + `)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gokit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return fmt.Errorf("missing command")
	}
	switch args[0] {
	case "new":
		return cmdNew(args[1:])
	case "sync":
		return cmdSync(args[1:])
	case "jwt-key":
		return cmdJWTKey(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	fmt.Print(usage)
	return fmt.Errorf("unknown command %q", args[0])
}

// splitArgs lets flags appear before or after the positional directory.
func splitArgs(fs *flag.FlagSet, args []string) (string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	if len(positional) != 1 {
		return "", fmt.Errorf("expected exactly one directory, got %d", len(positional))
	}
	return positional[0], nil
}

func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	var o Options
	fs.StringVar(&o.Name, "name", "", "")
	fs.StringVar(&o.Module, "module", "", "")
	fs.StringVar(&o.KitMode, "kit-mode", "local", "")
	fs.StringVar(&o.KitSrc, "kit-src", "", "")
	fs.StringVar(&o.KitModule, "kit-module", "", "")
	fs.StringVar(&o.KitVersion, "kit-version", "v0.1.0", "")
	fs.StringVar(&o.GoImage, "go-image", defaultGoImage, "")
	fs.StringVar(&o.DBImage, "db-image", defaultDBImage, "")
	fs.StringVar(&o.LintImage, "lint-image", defaultLintImage, "")
	fs.StringVar(&o.RedisImage, "redis-image", defaultRedisImage, "")
	dir, err := splitArgs(fs, args)
	if err != nil {
		return err
	}
	o.Dir = dir
	if err := Generate(&o); err != nil {
		return err
	}
	fmt.Printf("created project %q (module %s, kit %s)\n", o.Name, o.Module, o.KitMode)
	return nil
}

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	src := fs.String("kit-src", "", "")
	dir, err := splitArgs(fs, args)
	if err != nil {
		return err
	}
	if *src == "" {
		return fmt.Errorf("--kit-src is required")
	}
	if err := copyKit(*src, dir+"/.kit"); err != nil {
		return err
	}
	fmt.Println("kit refreshed in", dir+"/.kit")
	return nil
}

func cmdJWTKey(args []string) error {
	fs := flag.NewFlagSet("jwt-key", flag.ContinueOnError)
	hs := fs.Bool("hs256", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hs {
		s, err := randomHex(32)
		if err != nil {
			return err
		}
		fmt.Println("JWT_SECRET=" + s)
		return nil
	}
	priv, pub, err := auth.GenerateEd25519()
	if err != nil {
		return err
	}
	oneLine := func(b []byte) string { return strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", `\n`) }
	fmt.Println(`JWT_PRIVATE_KEY="` + oneLine(priv) + `"`)
	fmt.Println(`# for services that only verify tokens:`)
	fmt.Println(`JWT_PUBLIC_KEY="` + oneLine(pub) + `"`)
	return nil
}

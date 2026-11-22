// Command migrate applies explicit SQL migrations as a separate one-shot process.
package main

import (
	"context"
	"fmt"
	"github.com/PouryDev/server-infrastructure/gokit/migrate"
	"github.com/PouryDev/storefront/migrations"
	"os"
	"time"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "migrate:", e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: migrate up | down | status | create NAME")
	}
	if args[0] == "create" {
		if len(args) != 2 {
			return fmt.Errorf("usage: migrate create NAME")
		}
		p, e := migrate.Create("migrations", args[1], time.Now())
		if e == nil {
			fmt.Println("created", p)
		}
		return e
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	r := migrate.Runner{URL: url, FS: migrations.FS}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch args[0] {
	case "up":
		list, e := r.Up(ctx)
		for _, name := range list {
			fmt.Println("applied", name)
		}
		return e
	case "down":
		name, e := r.Down(ctx)
		if e == nil {
			fmt.Println("rolled back", name)
		}
		return e
	case "status":
		list, e := r.Status(ctx)
		for _, s := range list {
			fmt.Println(s.Name, s.Applied)
		}
		return e
	}
	return fmt.Errorf("unknown migration command")
}

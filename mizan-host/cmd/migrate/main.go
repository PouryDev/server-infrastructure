// Command migrate manages the database schema. It is a separate one-shot command (not run on API
// start-up) so that replicas never migrate concurrently.
//
//	migrate up (host schema, then mizan's) | down | status | create <name> | diff <name> [--concurrent-indexes] [--allow-drop] | drift
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/migrate"

	"github.com/PouryDev/mizan-host/internal/adapter/mizanmod"
	pgadapter "github.com/PouryDev/mizan-host/internal/adapter/postgres"
	"github.com/PouryDev/mizan-host/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: migrate up | down | status | create <name> | diff <name> | drift")
	}
	if args[0] == "create" { // writes into ./migrations, needs no database
		if len(args) != 2 {
			return fmt.Errorf("usage: migrate create <name>")
		}
		path, err := migrate.Create("migrations", args[1], time.Now())
		if err != nil {
			return err
		}
		fmt.Println("created", path)
		return nil
	}

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	r := migrate.Runner{URL: url, FS: migrations.FS}
	ctx := context.Background()

	switch args[0] {
	case "diff": // models -> new migration file (needs a database role that may CREATE DATABASE: development)
		if len(args) < 2 {
			return fmt.Errorf("usage: migrate diff <name> [--concurrent-indexes] [--allow-drop]")
		}
		opts := migrate.DiffOptions{URL: url, FS: migrations.FS, Models: pgadapter.Models(), OutDir: "migrations", Name: args[1]}
		for _, flag := range args[2:] {
			switch flag {
			case "--concurrent-indexes":
				opts.ConcurrentIndexes = true
			case "--allow-drop":
				opts.AllowDrop = true
			default:
				return fmt.Errorf("unknown flag %q", flag)
			}
		}
		res, err := migrate.Diff(ctx, opts)
		if err != nil {
			return err
		}
		for _, w := range res.Plan.Warnings {
			fmt.Println("note:", w)
		}
		if res.Plan.Empty() {
			fmt.Println("models and migrations already match, nothing to do")
			return nil
		}
		for _, p := range []string{res.Path, res.ConcurrentPath} {
			if p != "" {
				fmt.Println("created", p, "- review it (and its WARNING comments) before applying")
			}
		}
		return nil
	case "drift": // CI check: fails when the models changed without a migration
		plan, err := migrate.Plan(ctx, migrate.DiffOptions{URL: url, FS: migrations.FS, Models: pgadapter.Models()})
		if err != nil {
			return err
		}
		if !plan.Empty() {
			for _, s := range plan.Up() {
				fmt.Println(s + ";")
			}
			return fmt.Errorf("models and migrations differ: run `make migrate-diff NAME=...`")
		}
		fmt.Println("no drift: models and migrations match")
		return nil
	case "up":
		applied, err := r.Up(ctx)
		for _, a := range applied {
			fmt.Println("applied", a)
		}
		if err != nil {
			return err
		}
		// mizan keeps its schema in its own goose table, so the two histories never collide.
		more, err := mizanmod.Migrate(ctx, url)
		for _, a := range more {
			fmt.Println("applied (mizan)", a)
		}
		if err == nil && len(applied)+len(more) == 0 {
			fmt.Println("nothing to apply")
		}
		return err
	case "down":
		name, err := r.Down(ctx)
		if name != "" {
			fmt.Println("rolled back", name)
		}
		return err
	case "status":
		list, err := r.Status(ctx)
		for _, s := range list {
			state := "pending"
			if s.Applied {
				state = "applied " + s.AppliedAt.Format(time.RFC3339)
			}
			fmt.Printf("%-50s %s\n", s.Name, state)
		}
		return err
	}
	return fmt.Errorf("unknown command %q", args[0])
}

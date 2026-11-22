package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	path, err := Create(dir, "Add_Users", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "20260102030405_add_users.sql" {
		t.Fatalf("path = %s", path)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "+goose Up") || !strings.Contains(string(body), "+goose Down") {
		t.Fatal("template incomplete")
	}
	if _, err := Create(dir, "Add_Users", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err == nil {
		t.Fatal("must not overwrite an existing file")
	}
	for _, bad := range []string{"", "../x", "with space", "1abc", "a-b"} {
		if _, err := Create(dir, bad, time.Now()); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestTableNameIsValidated(t *testing.T) {
	r := Runner{URL: "postgres://x/y", FS: os.DirFS(t.TempDir())}
	for _, bad := range []string{"a;drop table x", "Upper", "with space", "1abc", `a"b`} {
		r.Table = bad
		if _, err := r.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "table name") {
			t.Errorf("table %q must be rejected before touching the database, got %v", bad, err)
		}
	}
}

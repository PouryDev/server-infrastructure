package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	_ "modernc.org/sqlite"
)

type Repository struct{ db *sql.DB }

func Open(path string) (*Repository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
		CREATE TABLE IF NOT EXISTS policies (project TEXT PRIMARY KEY, payload TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS audit (id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL, actor TEXT NOT NULL, project TEXT NOT NULL, action TEXT NOT NULL, outcome TEXT NOT NULL, detail TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS audit_project_id ON audit(project,id);
		CREATE TABLE IF NOT EXISTS shipping_target (id INTEGER PRIMARY KEY CHECK(id=1), payload TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS shipping_plans (id TEXT PRIMARY KEY, payload TEXT NOT NULL, bundle BLOB NOT NULL);
		CREATE TABLE IF NOT EXISTS shipping_jobs (id TEXT PRIMARY KEY, plan_id TEXT UNIQUE NOT NULL, status TEXT NOT NULL, payload TEXT NOT NULL);
		CREATE UNIQUE INDEX IF NOT EXISTS shipping_one_active ON shipping_jobs((1)) WHERE status IN ('queued','running');
		PRAGMA user_version=2;`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize manager database: %w", err)
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error { return r.db.Close() }

func (r *Repository) Policies(ctx context.Context) (map[string]domain.Policy, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT payload FROM policies")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]domain.Policy{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var p domain.Policy
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return nil, err
		}
		result[p.Project] = p
	}
	return result, rows.Err()
}

func (r *Repository) Save(ctx context.Context, p domain.Policy, e domain.Event) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO policies(project,payload) VALUES(?,?) ON CONFLICT(project) DO UPDATE SET payload=excluded.payload", p.Project, string(payload)); err != nil {
		return err
	}
	if err = insert(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insert(ctx context.Context, db execer, e domain.Event) error {
	_, err := db.ExecContext(ctx, "INSERT INTO audit(at,actor,project,action,outcome,detail) VALUES(?,?,?,?,?,?)", e.At.UTC().Format(time.RFC3339Nano), e.Actor, e.Project, e.Action, e.Outcome, e.Detail)
	return err
}
func (r *Repository) Record(ctx context.Context, e domain.Event) error { return insert(ctx, r.db, e) }

func (r *Repository) Audit(ctx context.Context, project string, before int64, limit int) ([]domain.Event, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := r.db.QueryContext(ctx, "SELECT id,at,actor,project,action,outcome,detail FROM audit WHERE (?='' OR project=?) AND id<? ORDER BY id DESC LIMIT ?", project, project, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Project, &e.Action, &e.Outcome, &e.Detail); err != nil {
			return nil, err
		}
		e.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

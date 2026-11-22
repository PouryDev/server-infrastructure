// Package audit appends who-did-what records to an immutable table, inside the caller's transaction so a
// business change and its audit record commit together.
package audit

import (
	"context"
	"encoding/json"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Log writes entries to mz_audit_log and implements trail.Recorder.
type Log struct{ db *postgres.DB }

var _ trail.Recorder = (*Log)(nil)

// New creates the log.
func New(db *postgres.DB) *Log { return &Log{db: db} }

// Record appends an entry; call it inside the same transaction as the change it describes.
func (l *Log) Record(ctx context.Context, e trail.Entry) error {
	if e.Actor == "" {
		e.Actor = "system"
	}
	d, err := json.Marshal(e.Details)
	if err != nil || e.Details == nil {
		d = []byte("{}")
	}
	_, err = postgres.Exec(ctx, l.db.Querier(ctx),
		`INSERT INTO mz_audit_log (actor, action, entity_type, entity_id, details) VALUES ($1, $2, $3, $4, $5)`,
		e.Actor, e.Action, e.EntityType, e.EntityID, d)
	return err
}

// Row is a stored record.
type Row struct {
	ID         int64           `db:"id" json:"id"`
	At         string          `db:"at" json:"at"`
	Actor      string          `db:"actor" json:"actor"`
	Action     string          `db:"action" json:"action"`
	EntityType string          `db:"entity_type" json:"entity_type"`
	EntityID   string          `db:"entity_id" json:"entity_id"`
	Details    json.RawMessage `db:"details" json:"details"`
}

// For returns the newest records of one entity (limit <= 500).
func (l *Log) For(ctx context.Context, entityType, entityID string, limit int) ([]Row, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return postgres.Select[Row](ctx, l.db.Querier(ctx), `
		SELECT id, to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS at, actor, action, entity_type, entity_id, details
		  FROM mz_audit_log WHERE entity_type = $1 AND entity_id = $2 ORDER BY id DESC LIMIT $3`, entityType, entityID, limit)
}

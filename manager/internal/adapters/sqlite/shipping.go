package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func (r *Repository) Target(ctx context.Context) (domain.ShipTarget, error) {
	var payload string
	err := r.db.QueryRowContext(ctx, "SELECT payload FROM shipping_target WHERE id=1").Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ShipTarget{Mode: "local", Port: 22, Identity: "id_ed25519", InfraDir: "/tmp/infra-manager-target/infrastructure", AppsDir: "/tmp/infra-manager-target/apps", DataDir: "/tmp/infra-manager-target/data"}, nil
	}
	var t domain.ShipTarget
	if err == nil {
		err = json.Unmarshal([]byte(payload), &t)
	}
	return t, err
}
func (r *Repository) SaveTarget(ctx context.Context, t domain.ShipTarget, e domain.Event) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO shipping_target(id,payload) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", string(data)); err != nil {
		return err
	}
	if err = insert(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) SavePlan(ctx context.Context, p domain.ShipPlan, e domain.Event) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO shipping_plans(id,payload,bundle) VALUES(?,?,?)", p.ID, string(data), p.Bundle); err != nil {
		return err
	}
	if err = insert(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) Plan(ctx context.Context, id string) (domain.ShipPlan, error) {
	var data string
	var bundle []byte
	err := r.db.QueryRowContext(ctx, "SELECT payload,bundle FROM shipping_plans WHERE id=?", id).Scan(&data, &bundle)
	var p domain.ShipPlan
	if err == nil {
		err = json.Unmarshal([]byte(data), &p)
		p.Bundle = bundle
	}
	return p, err
}
func (r *Repository) QueueShip(ctx context.Context, j domain.ShipJob, e domain.Event) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO shipping_jobs(id,plan_id,status,payload) VALUES(?,?,?,?)", j.ID, j.PlanID, j.Status, string(data)); err != nil {
		return fmt.Errorf("a shipment is already active or this preview was already used")
	}
	if err = insert(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) SaveShip(ctx context.Context, j domain.ShipJob, e domain.Event) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE shipping_jobs SET status=?,payload=? WHERE id=?", j.Status, string(data), j.ID); err != nil {
		return err
	}
	if err = insert(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) ShipJobs(ctx context.Context) ([]domain.ShipJob, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT payload FROM shipping_jobs ORDER BY rowid DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []domain.ShipJob{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var j domain.ShipJob
		if err = json.Unmarshal([]byte(data), &j); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

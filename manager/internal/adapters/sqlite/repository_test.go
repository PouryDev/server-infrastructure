package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func TestAuditIsAppendOnlyPaginatedAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := range 4 {
		if err = repo.Save(ctx, domain.Policy{Project: "shop", Revision: int64(i)}, domain.Event{At: time.Now(), Actor: "admin", Project: "shop", Action: "renew", Outcome: "accepted"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Record(ctx, domain.Event{At: time.Now(), Project: "other", Action: "login"}); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	repo, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	first, err := repo.Audit(ctx, "shop", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Audit(ctx, "shop", first[1].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 || first[1].ID <= second[0].ID {
		t.Fatalf("invalid audit pagination: %+v %+v", first, second)
	}
	policies, err := repo.Policies(ctx)
	if err != nil || policies["shop"].Revision != 3 {
		t.Fatalf("policy not persisted: %v %+v", err, policies)
	}
}

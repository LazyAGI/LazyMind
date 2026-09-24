package artifact

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// Hold one caller-owned transaction open until PostgreSQL reports that the
// competing writer is blocked. This exercises the missing-row creation race
// deterministically, rather than hoping goroutines happen to overlap.
func TestPostgresCallerTransactionSerializesLogicalArtifact(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		existing, replay, rollback bool
	}{
		{name: "new"},
		{name: "existing", existing: true},
		{name: "idempotent_existing", existing: true, replay: true},
		{name: "rolled_back_creation", rollback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := v2TestDB(t).DB
			if db.Dialector.Name() != orm.DriverPostgres {
				t.Skip("requires TEST_DB_DRIVER=postgres and TEST_DB_DSN")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req := CommitRequest{OwnerUserID: "owner", LogicalKey: "shared", ContentType: "text", InlineJSON: []byte(`{"text":"first"}`)}
			want := int64(2)
			if tc.existing {
				if _, err := New(db).CommitRevision(ctx, req); err != nil {
					t.Fatal(err)
				}
				want++
			}
			if tc.replay {
				req.IdempotencyKey = "same-delivery"
				want--
			}
			if tc.rollback {
				want--
			}
			firstTx := db.WithContext(ctx).Begin()
			if firstTx.Error != nil {
				t.Fatal(firstTx.Error)
			}
			defer firstTx.Rollback()
			var firstPID int
			if err := firstTx.Raw("SELECT pg_backend_pid()").Scan(&firstPID).Error; err != nil {
				t.Fatal(err)
			}
			first, err := InTransaction(firstTx).CommitRevision(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			secondTx := db.WithContext(ctx).Begin()
			if secondTx.Error != nil {
				t.Fatal(secondTx.Error)
			}
			defer secondTx.Rollback()
			var secondPID int
			if err := secondTx.Raw("SELECT pg_backend_pid()").Scan(&secondPID).Error; err != nil {
				t.Fatal(err)
			}
			secondReq := req
			if !tc.replay {
				secondReq.InlineJSON = []byte(`{"text":"second"}`)
			}
			type result struct {
				view *RevisionView
				err  error
			}
			done := make(chan result, 1)
			go func() {
				view, err := InTransaction(secondTx).CommitRevision(ctx, secondReq)
				if err == nil {
					err = secondTx.Commit().Error
				} else {
					secondTx.Rollback()
				}
				done <- result{view, err}
			}()
			for {
				var blocked bool
				if err := db.WithContext(ctx).Raw("SELECT ? = ANY(pg_blocking_pids(?))", firstPID, secondPID).Scan(&blocked).Error; err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case result := <-done:
					t.Fatalf("second writer did not wait: %+v", result)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if tc.rollback {
				err = firstTx.Rollback().Error
			} else {
				err = firstTx.Commit().Error
			}
			if err != nil {
				t.Fatal(err)
			}
			var second result
			select {
			case second = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if second.err != nil {
				t.Fatalf("competing commit: %v", second.err)
			}
			if tc.replay && first.RevisionID != second.view.RevisionID {
				t.Fatal("replay created another revision")
			}
			if !tc.rollback && first.ArtifactID != second.view.ArtifactID {
				t.Fatal("writers created different artifacts")
			}
			for _, model := range []any{&orm.ArtifactRevision{}, &orm.ArtifactEventOutbox{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != want {
					t.Fatalf("%T count=%d, want %d", model, count, want)
				}
			}
			head, err := New(db).Head(ctx, second.view.ArtifactID, ChannelPublished)
			if err != nil || head.RevisionID != second.view.RevisionID || head.Version != want {
				t.Fatalf("head=%+v err=%v", head, err)
			}
		})
	}
}

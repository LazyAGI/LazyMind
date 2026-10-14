package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	taskpkg "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestWorkerKeepsPersistedNewAndLegacyCreatorPolicies(t *testing.T) {
	for _, name := range []string{"admin", ""} {
		for _, action := range []string{statepkg.PendingActionCreate, statepkg.PendingActionReparse} {
			t.Run(name+"/"+action, func(t *testing.T) {
				now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
				repo := newWorkerIdempotencyStore(now)
				source := repo.sources["source-1"]
				source.CreatedBy = "persisted-source-owner"
				if name != "" {
					source.SourceOptions = store.JSON{"created_by_name": name}
				}
				repo.sources["source-1"] = source
				state := statepkg.SourceStateNew
				if action == statepkg.PendingActionReparse {
					state = statepkg.SourceStateModified
				}
				task := repo.seedPendingTask("creator-fixture", "v1", state, action)
				if action == statepkg.PendingActionReparse {
					task.TaskAction = taskpkg.TaskActionReparse
					task.IdempotencyKey = taskpkg.IdempotencyKey(task)
					repo.tasks[task.TaskID] = task
					docKey := workerIdempotencyKey("source-1", "binding-1", "creator-fixture")
					doc := repo.documents[docKey]
					doc.CoreDocumentID = "legacy-core-document"
					doc.CurrentVersionID = "v0"
					repo.documents[docKey] = doc
				}
				conn := &workerSpyConnector{exportVersion: "v1"}
				core := newWorkerIdempotencyCoreClient()
				reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
				parser := worker.NewDefaultParseWorker(repo, mustRegistry(t, conn), core, reducer,
					&workerIdempotencyTempObjectStore{Objects: map[string][]byte{"creator-fixture": []byte("synthetic content")}},
					worker.WithClock(func() time.Time { return now }))
				if err := parser.RunOnce(context.Background(), "worker-fixture"); err != nil {
					t.Fatal(err)
				}
				if len(core.Submissions) != 1 {
					t.Fatalf("expected one actual Core submission, got %d", len(core.Submissions))
				}
				submission := core.Submissions[0]
				if submission.UserID != source.CreatedBy || submission.UserName != name {
					t.Fatalf("worker must retain persisted creator policy: %+v want ID=%q name=%q", submission, source.CreatedBy, name)
				}
				if action == statepkg.PendingActionReparse && submission.Action != coreclient.ActionReparse {
					t.Fatal("modified source must retain reparse action")
				}
			})
		}
	}
}

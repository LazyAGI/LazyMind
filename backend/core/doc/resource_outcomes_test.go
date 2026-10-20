package doc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func TestResourceMoveIndependentRootsPartialReplay(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	s.budget.Calibrated = true
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	var err error
	s.originals, err = NewOriginalService(OriginalServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"good/empty", "bad"}})
	if err != nil {
		t.Fatal(err)
	}
	good, bad := created.Items[0].ResourceRef, created.Items[2].ResourceRef
	seedDocumentListDoc(t, db, "source", "missing-original", "missing.txt", time.Now().UTC(), "user-1", nil)
	db.Model(&orm.Document{}).Where("id = ?", "missing-original").Update("p_id", bad.ID())
	op.OperationKey = "partial-roots"
	req := MoveResourcesRequest{Sources: []ResourceRef{good, bad}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}}
	result, err := s.Move(ctx, op, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial" || result.Summary.Succeeded != 1 || result.Summary.Failed != 1 {
		t.Fatalf("incorrect partial result: %+v", result)
	}
	var source, target int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "source").Count(&source)
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "target").Count(&target)
	if source != 2 || target != 2 {
		t.Fatalf("wrong root isolation: source=%d target=%d", source, target)
	}
	// Remove the optional aggregate receipt to simulate interruption after child commits.
	db.Where("operation_key = ?", op.OperationKey).Delete(&orm.ResourceOperationReceipt{})
	if committed, e := s.HasCommittedMove(ctx, op, req); e != nil || !committed {
		t.Fatalf("committed child receipt not recognized: %v %v", committed, e)
	}
	replay, err := s.Move(ctx, op, req)
	if err != nil || resourceHash(replay) != resourceHash(result) {
		t.Fatalf("child replay changed result: %+v %v", replay, err)
	}
	db.Where("operation_key = ?", op.OperationKey).Delete(&orm.ResourceOperationReceipt{})
	req.Sources = []ResourceRef{bad}
	_, err = s.Move(ctx, op, req)
	var typed *ResourceError
	if !errors.As(err, &typed) || typed.Code != "CONFLICT" {
		t.Fatalf("changed batch input accepted: %v", err)
	}
}

type resourceCommitFailurePool struct {
	gorm.ConnPool
	commit bool
}

func (p resourceCommitFailurePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &resourceCommitFailureTx{Tx: tx, commit: p.commit}, nil
}

type resourceCommitFailureTx struct {
	*sql.Tx
	commit bool
}

func (tx resourceCommitFailureTx) Commit() error {
	if tx.commit {
		if err := tx.Tx.Commit(); err != nil {
			return err
		}
	} else {
		_ = tx.Tx.Rollback()
	}
	return io.ErrUnexpectedEOF
}
func TestResourceCommitLostAcknowledgementReconcilesReceipt(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(map[bool]string{true: "committed", false: "unknown"}[committed], func(t *testing.T) {
			s, db, op := resourceFixture(t)
			op.OperationKey = "commit-ack"
			wrapped := db.DB.Session(&gorm.Session{NewDB: true, Context: context.Background()})
			wrapped.Statement.ConnPool = resourceCommitFailurePool{ConnPool: db.Statement.ConnPool, commit: committed}
			s.db = wrapped
			result, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"folder"}})
			if committed {
				if err != nil || len(result.Items) != 1 || result.Items[0].ResourceStatus != "created" {
					t.Fatalf("lost acknowledged result: %+v %v", result, err)
				}
				var row orm.Document
				if err = db.First(&row, "id = ?", result.Items[0].ResourceRef.ID()).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if !resourceOutcomeUnconfirmed(err) || result.Status != "unconfirmed" || result.Summary.Unconfirmed != 1 {
					t.Fatalf("ambiguous commit claimed definitive result: %+v %v", result, err)
				}
			}
		})
	}
}

func TestResourceMoveMetadataRollsBackOnDestinationConflict(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"from", "to/existing"}})
	if err != nil {
		t.Fatal(err)
	}
	name := "existing"
	_, err = s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{created.Items[0].ResourceRef}, Destination: created.Items[1].ResourceRef, PageMetadata: &ResourceMetadataChanges{Name: &name}})
	if err == nil {
		t.Fatal("destination name conflict accepted")
	}
	var row orm.Document
	if err = db.First(&row, "id = ?", created.Items[0].ResourceRef.ID()).Error; err != nil {
		t.Fatal(err)
	}
	if row.DisplayName != "from" || row.PID != "" {
		t.Fatalf("failed move changed metadata: %+v", row)
	}
}

func TestResourceExecutionFenceRunsBeforeMutation(t *testing.T) {
	s, db, op := resourceFixture(t)
	op.OperationKey = "expired-run"
	calls := 0
	op.ValidateExecution = func(ctx context.Context, tx *gorm.DB) error {
		calls++
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			t.Fatal("execution guard was called outside transaction")
		}
		return resourceErr("FORBIDDEN", "run ended")
	}
	_, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"denied"}})
	if err == nil || calls != 1 {
		t.Fatalf("execution guard bypassed: calls=%d err=%v", calls, err)
	}
	var documents, receipts int64
	db.Model(&orm.Document{}).Count(&documents)
	db.Model(&orm.ResourceOperationReceipt{}).Count(&receipts)
	if documents != 0 || receipts != 0 {
		t.Fatalf("ended run committed resources: %d receipts: %d", documents, receipts)
	}
}

func TestResourceSnapshotPreviewAndResumeReceipt(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	made, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"outer/inner"}})
	if err != nil {
		t.Fatal(err)
	}
	req := MoveResourcesRequest{Sources: []ResourceRef{made.Items[1].ResourceRef}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}}
	snap, err := s.SnapshotMove(ctx, op, req)
	if err != nil || len(snap.PreviewResources) != 1 || !strings.HasSuffix(snap.PreviewResources[0].Path, "/outer/inner") || snap.Destination == nil || snap.Destination.Ref != req.Destination {
		t.Fatalf("bad preview: %+v %v", snap, err)
	}
	op.OperationKey = "header-only"
	header := resourceMoveBatch{Roots: req.Sources}
	raw, _ := json.Marshal(header)
	db.Create(&orm.ResourceOperationReceipt{TenantID: resourceTenant(op.Caller.TenantID), UserID: op.Caller.UserID, OperationKey: "move:" + resourceHash([]string{op.OperationKey})[:40] + ":header", Action: "knowledge.resource.move.header", ParameterHash: resourceOperationHash(op, req), Result: raw, CreatedAt: time.Now().UTC()})
	committed, err := s.HasCommittedMove(ctx, op, req)
	if err != nil || committed {
		t.Fatalf("header without committed root bypassed preflight: %v %v", committed, err)
	}
}

func TestResourceCreateKnowledgeCommitsOwnerMembership(t *testing.T) {
	s, db, op := resourceFixture(t)
	if err := db.AutoMigrate(&orm.KBModel{}, &orm.ACLModel{}); err != nil {
		t.Fatal(err)
	}
	s.provision = func(context.Context, DatasetCatalogCaller, *orm.Dataset) error { return nil }
	op.OperationKey = "create-owner"
	result, err := s.CreateKnowledge(context.Background(), op, CreateKnowledgeRequest{Name: "New-library"})
	if err != nil {
		t.Fatal(err)
	}
	id := result.Items[0].ResourceRef.ID()
	var kb orm.KBModel
	if err = db.First(&kb, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if kb.OwnerID != op.Caller.UserID {
		t.Fatalf("missing owner: %+v", kb)
	}
	var grants []orm.ACLModel
	db.Where("resource_type = ? AND resource_id = ? AND target_id = ?", acl.ResourceTypeDB, id, op.Caller.UserID).Find(&grants)
	if len(grants) != len(roleToPermissions("dataset_maintainer")) {
		t.Fatalf("missing creator membership: %+v", grants)
	}
}

func TestResourceFreshAccountGuardPrecedesTransaction(t *testing.T) {
	s, db, op := resourceFixture(t)
	calls := 0
	op.ValidateBeforeCommit = func(context.Context) error { calls++; return resourceErr("FORBIDDEN", "account disabled") }
	op.ValidateExecution = func(context.Context, *gorm.DB) error {
		t.Fatal("opened resource transaction after account rejection")
		return nil
	}
	_, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"denied"}})
	if err == nil || calls != 1 {
		t.Fatalf("fresh identity guard bypassed: calls=%d err=%v", calls, err)
	}
	var n int64
	db.Model(&orm.Document{}).Count(&n)
	if n != 0 {
		t.Fatal("disabled account mutated resources")
	}
}

func TestResourceSnapshotBudgetCountsNormalizedRootsOnce(t *testing.T) {
	s, _, op := resourceFixture(t)
	ctx := context.Background()
	made, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"parent/child"}})
	if err != nil {
		t.Fatal(err)
	}
	s.budget.MaxResources = 2
	snap, err := s.Snapshot(ctx, op, []ResourceRef{made.Items[0].ResourceRef, made.Items[1].ResourceRef})
	if err != nil || snap.FolderCount != 2 {
		t.Fatalf("ancestor duplicate inflated budget: %+v %v", snap, err)
	}
}

func TestResourceBatchMoveRechecksConfirmedHeaderInsideRootTransaction(t *testing.T) {
	for _, change := range []string{"new-descendant", "root-rename", "destination-rename"} {
		t.Run(change, func(t *testing.T) {
			s, db, op := resourceFixture(t)
			ctx := context.Background()
			s.budget.Calibrated = true
			var err error
			s.originals, err = NewOriginalService(OriginalServiceDeps{DB: db.DB})
			if err != nil {
				t.Fatal(err)
			}
			made, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"first", "second"}})
			if err != nil {
				t.Fatal(err)
			}
			destination, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "target", Folders: []string{"destination"}})
			if err != nil {
				t.Fatal(err)
			}
			req := MoveResourcesRequest{Sources: []ResourceRef{made.Items[0].ResourceRef, made.Items[1].ResourceRef}, Destination: destination.Items[0].ResourceRef}
			snapshot, err := s.SnapshotMove(ctx, op, req)
			if err != nil {
				t.Fatal(err)
			}
			op.OperationKey = "confirmed-batch"
			op.ExpectedSnapshot = snapshot.Digest
			armed, injected := false, false
			s.sourceAccess = func(_ context.Context, _ DatasetCatalogCaller, dataset, action string) error {
				// The first target read after the durable header is the wrapper's
				// destination check. Inject at the following source read, which starts
				// moveAtomic's fresh tree, so its own preparation snapshot includes the
				// unconfirmed change and cannot catch this race by itself.
				if dataset == "target" {
					var headers int64
					db.Model(&orm.ResourceOperationReceipt{}).Where("action = ?", "knowledge.resource.move.header").Count(&headers)
					if headers > 0 {
						armed = true
					}
				} else if dataset == "source" && armed && !injected {
					injected = true
					switch change {
					case "new-descendant":
						err = db.Create(&orm.Document{ID: "late-child", DatasetID: "source", PID: made.Items[0].ResourceRef.ID(), DisplayName: "late", DocumentType: "FOLDER", ResourceVersion: 1}).Error
					case "root-rename":
						err = db.Model(&orm.Document{}).Where("id = ?", made.Items[0].ResourceRef.ID()).Update("display_name", "renamed").Error
					case "destination-rename":
						err = db.Model(&orm.Document{}).Where("id = ?", destination.Items[0].ResourceRef.ID()).Update("display_name", "renamed-target").Error
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			result, err := s.Move(ctx, op, req)
			if err != nil {
				t.Fatal(err)
			}
			if !injected {
				t.Fatal("race hook was not exercised")
			}
			if len(result.Items) != 2 || result.Items[0].ResourceStatus != "failed" || result.Items[0].Error == nil || result.Items[0].Error.Code != "CONFLICT" {
				t.Fatalf("unconfirmed change was moved: %+v", result)
			}
			var source int64
			db.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", made.Items[0].ResourceRef.ID(), "source").Count(&source)
			if source != 1 {
				t.Fatal("changed root did not remain at source")
			}
			if change == "destination-rename" && result.Summary.Succeeded != 0 {
				t.Fatalf("changed destination accepted: %+v", result)
			}
		})
	}
}

func TestResourceCrossMoveReservesCommitReceiptDeadline(t *testing.T) {
	s, db, op := resourceFixture(t)
	s.budget.Calibrated = true
	var err error
	s.originals, err = NewOriginalService(OriginalServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	made, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"folder"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{made.Items[0].ResourceRef}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}})
	var typed *ResourceError
	if !errors.As(err, &typed) || typed.Code != "OPERATION_TOO_LARGE" {
		t.Fatalf("short request deadline did not reserve receipt time: %v", err)
	}
	var source, target int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "source").Count(&source)
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "target").Count(&target)
	if source != 1 || target != 0 {
		t.Fatalf("short deadline changed source: source=%d target=%d", source, target)
	}
}

func TestResourceMoveDoesNotReplaceConfirmedDestinationDuringPreflight(t *testing.T) {
	for _, roots := range []int{1, 2} {
		t.Run(fmt.Sprint(roots), func(t *testing.T) {
			s, db, op := resourceFixture(t)
			ctx := context.Background()
			s.budget.Calibrated = true
			var err error
			s.originals, err = NewOriginalService(OriginalServiceDeps{DB: db.DB})
			if err != nil {
				t.Fatal(err)
			}
			made, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"first", "second"}})
			if err != nil {
				t.Fatal(err)
			}
			refs := []ResourceRef{made.Items[0].ResourceRef}
			if roots == 2 {
				refs = append(refs, made.Items[1].ResourceRef)
			}
			req := MoveResourcesRequest{Sources: refs, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}}
			snapshot, err := s.SnapshotMove(ctx, op, req)
			if err != nil {
				t.Fatal(err)
			}
			op.ExpectedSnapshot = snapshot.Digest
			op.OperationKey = "destination-during-preflight"
			changed := false
			s.sourceAccess = func(_ context.Context, _ DatasetCatalogCaller, id, action string) error {
				if id == "target" && !changed {
					changed = true
					// Dataset resolution has read its old row before calling source policy.
					// A separate subsequent destination capture would accept this new name.
					if err := db.Model(&orm.Dataset{}).Where("id = ?", "target").Update("display_name", "renamed-target").Error; err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			result, err := s.Move(ctx, op, req)
			if err == nil && result.Summary.Succeeded != 0 {
				t.Fatalf("confirmation silently rebound to changed destination: %+v", result)
			}
			if err != nil {
				var typed *ResourceError
				if !errors.As(err, &typed) || typed.Code != "CONFLICT" {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			var targets int64
			db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "target").Count(&targets)
			if !changed || targets != 0 {
				t.Fatalf("destination change was accepted: changed=%v targets=%d", changed, targets)
			}
		})
	}
}

func TestResourceCreateKnowledgeResolvesDefaultAlgorithm(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *Dataset
		want    string
	}{
		{"tool", nil, defaultMarketAlgoID},
		{"page_default", &Dataset{}, defaultMarketAlgoID},
		{"page_explicit", &Dataset{Algo: Algo{AlgoID: "custom_algo", DisplayName: "Custom"}}, "custom_algo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, op := resourceFixture(t)
			if err := db.AutoMigrate(&orm.KBModel{}, &orm.ACLModel{}); err != nil {
				t.Fatal(err)
			}
			s.provision = func(_ context.Context, _ DatasetCatalogCaller, ds *orm.Dataset) error {
				if got := parseDatasetAlgo(ds.Ext).AlgoID; got != tc.want {
					return fmt.Errorf("upstream invalid algo_id: %q; want %q", got, tc.want)
				}
				return nil
			}
			result, err := s.CreateKnowledge(context.Background(), op, CreateKnowledgeRequest{Name: "Default-algo", PageOptions: tc.options})
			if err != nil {
				t.Fatal(err)
			}
			var ds orm.Dataset
			if err = db.First(&ds, "id = ?", result.Items[0].ResourceRef.ID()).Error; err != nil {
				t.Fatal(err)
			}
			if got := parseDatasetAlgo(ds.Ext).AlgoID; got != tc.want {
				t.Fatalf("persisted algo=%q want %q", got, tc.want)
			}
		})
	}
}

package documentdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestFolderCollectionClassificationAndRetry(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	result, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_begin"}, FolderRequest{Source: encoded(map[string]string{"provider": "local", "root": "/fixture"}), State: encoded(map[string]any{"queue": []any{}, "errors": []any{}})})
	must(t, err)
	inv := result["inventory"].(orm.DocumentFolderInventory)
	nodes := []orm.DocumentFolderNode{{NodeKey: "root", Path: "", Name: "fixture", Kind: "directory", Listed: true}}
	for i := 0; i < 13; i++ {
		nodes = append(nodes, orm.DocumentFolderNode{NodeKey: fmt.Sprint("dir", i), ParentKey: "root", Path: fmt.Sprint("d", i), Name: fmt.Sprint("d", i), Kind: "directory", Listed: true})
	}
	for i := 0; i < 119; i++ {
		c := sample(fmt.Sprint("/fixture/d0/", i))
		nodes = append(nodes, orm.DocumentFolderNode{NodeKey: fmt.Sprint("file", i), ParentKey: "dir0", Path: fmt.Sprint("d0/", i), Name: fmt.Sprint(i), Kind: "file", CandidateJSON: encoded(c)})
	}
	_, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_submit", Name: "fixture"}, FolderRequest{InventoryID: inv.ID})
	if err == nil {
		t.Fatal("incomplete inventory accepted")
	}
	must(t, SaveObservations(ctx, db, "u", "conv", "h", "r", "folder", []Observation{{InventoryID: inv.ID, CallID: "sample", Operation: "read", Candidates: []Candidate{sample("/fixture/d0/0"), sample("/unrelated")}}}))
	var held orm.DocumentObservation
	must(t, db.Where("conversation_id=?", "conv").Take(&held).Error)
	if held.BatchID == "" || len(held.BatchID) > 36 {
		t.Fatal("pending evidence marker must fit the PostgreSQL batch_id column")
	}
	_, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_checkpoint"}, FolderRequest{InventoryID: inv.ID, Version: 1, Status: "complete", State: encoded(map[string]any{"queue": []any{}, "errors": []any{}}), Nodes: nodes})
	must(t, err)
	submit := func() map[string]any {
		v, e := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_submit", Name: "fixture"}, FolderRequest{InventoryID: inv.ID})
		must(t, e)
		return v
	}
	first, second := submit(), submit()
	var observations []orm.DocumentObservation
	must(t, db.Find(&observations).Error)
	if len(observations) != 2 {
		t.Fatal("mixed observations were not separated")
	}
	for _, observation := range observations {
		var payload Observation
		must(t, json.Unmarshal(observation.PayloadJSON, &payload))
		if payload.InventoryID != "" && observation.BatchID != first["batch_id"] || payload.InventoryID == "" && observation.BatchID != "" {
			t.Fatal("folder evidence leaked into idle or unrelated discovery was consumed")
		}
	}
	if first["directory_id"] != second["directory_id"] {
		t.Fatal("duplicate submit")
	}
	var b orm.DocumentDirectoryBatch
	must(t, db.Where("id=?", first["batch_id"]).Take(&b).Error)
	var input Input
	must(t, json.Unmarshal(b.InputJSON, &input))
	until := time.Now().UTC().Add(time.Minute)
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", b.JobID).Updates(map[string]any{"status": "running", "attempt_count": 1, "lock_until": until}).Error)
	j := asyncjob.Job{ID: b.JobID, JobType: OrganizeJob, CreateUserID: "u", AttemptCount: 1}
	calls := 0
	generate := func(_ string, data any) (Proposal, error) {
		calls++
		if calls == 2 {
			return Proposal{}, fmt.Errorf("injected model timeout")
		}
		resources := data.(map[string]any)["resources"].(map[string]Candidate)
		if len(resources) > 30 {
			t.Fatal("unbounded classification")
		}
		// Omit every assignment: collection must still retain all references.
		return Proposal{Groups: []orm.DocumentDirectoryGroup{}, Assignments: []Assignment{}}, nil
	}
	if err = organizeFolder(ctx, db, j, &b, input, generate); err == nil {
		t.Fatal("expected failure")
	}
	var count int64
	must(t, db.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=?", b.DirectoryID).Count(&count).Error)
	if count != 119 {
		t.Fatalf("collected %d", count)
	}
	var progress struct {
		Processed int `json:"processed"`
	}
	must(t, json.Unmarshal(b.ResultJSON, &progress))
	if progress.Processed != 30 {
		t.Fatalf("progress %d", progress.Processed)
	}
	must(t, organizeFolder(ctx, db, j, &b, input, generate))
	if calls != 5 {
		t.Fatalf("replayed batches: %d", calls)
	}
	d, err := Get(ctx, db, "u", b.DirectoryID, EntryPageQuery{Limit: 25, Offset: 100})
	must(t, err)
	if !d.Directory.Pinned || d.Directory.AutoAdd || len(d.Entries) != 19 || d.Total != 119 {
		t.Fatalf("invalid collected directory: %+v", d.Directory)
	}
	tree, err := FolderNodes(ctx, db, "u", b.DirectoryID, "root", 0)
	must(t, err)
	if tree["total"].(int64) != 13 {
		t.Fatal("lost subdirectories")
	}
	if _, err = FolderNodes(ctx, db, "other", b.DirectoryID, "root", 0); err == nil {
		t.Fatal("cross-user tree access")
	}
	must(t, db.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=?", b.DirectoryID).Count(&count).Error)
	if count != 119 {
		t.Fatal("classification removed references")
	}
}

func TestFolderConcurrentSubmissionsReserveOnlyAvailablePins(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for i := 0; i < 9; i++ {
		d := create(t, db, fmt.Sprint("existing", i))
		must(t, db.Model(&d).Update("pinned", true).Error)
	}
	ids := []string{}
	for i := 0; i < 2; i++ {
		v, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_begin"}, FolderRequest{Source: encoded(map[string]string{"provider": "local", "root": "/empty"}), State: encoded(map[string]any{})})
		must(t, err)
		inv := v["inventory"].(orm.DocumentFolderInventory)
		_, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_checkpoint"}, FolderRequest{InventoryID: inv.ID, Version: 1, Status: "complete", State: encoded(map[string]any{"queue": []any{}, "errors": []any{}}), Nodes: []orm.DocumentFolderNode{{NodeKey: "root", Kind: "directory", Listed: true, Name: "empty"}}})
		must(t, err)
		ids = append(ids, inv.ID)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_submit", Name: "empty"}, FolderRequest{InventoryID: id})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrPinLimit) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatalf("success=%d limited=%d", success, limited)
	}
	var pins int64
	must(t, db.Model(&orm.DocumentDirectory{}).Where("user_id=? AND pinned=?", "u", true).Count(&pins).Error)
	if pins != 10 {
		t.Fatalf("pins=%d", pins)
	}
}

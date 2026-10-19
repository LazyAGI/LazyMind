package documentdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestFolderObservationMembershipBatchesAndRetry(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprint(deferred), func(t *testing.T) {
			db := testDB(t)
			v := orm.DocumentFolderInventory{ID: "inventory", UserID: "u", ConversationID: "conv", Status: "complete", SourceJSON: encoded(map[string]any{"provider": "local", "namespace": "host-a"}), StateJSON: encoded(map[string]any{"node_format_version": 2})}
			if deferred {
				v.Status = "scanning"
			}
			must(t, db.Create(&v).Error)
			items := []Candidate{}
			for i := 0; i < 205; i++ {
				c := sample(fmt.Sprintf("/file/%d", i))
				items = append(items, c)
				must(t, db.Create(&orm.DocumentFolderNode{ID: fmt.Sprint(i), InventoryID: v.ID, NodeKey: fmt.Sprint(i), ResourceKey: resourceKey(c)}).Error)
			}
			items = append(items, sample("/outside"), items[0])
			queries := 0
			must(t, db.Callback().Query().After("gorm:query").Register("membership_queries", func(tx *gorm.DB) {
				if tx.Statement.Table == "document_folder_nodes" {
					queries++
				}
			}))
			obs := Observation{InventoryID: v.ID, CallID: "read:" + v.ID, Candidates: items}
			for retry := 0; retry < 2; retry++ {
				must(t, SaveObservations(context.Background(), db, "u", "conv", "h", "r", "question", []Observation{obs}))
				if deferred {
					v.Status = "complete"
					must(t, db.Save(&v).Error)
					must(t, reconcileFolderObservations(db, v))
				}
			}
			if queries > 9 {
				t.Fatalf("membership still queries per candidate: %d", queries)
			}
			var rows []orm.DocumentObservation
			must(t, db.Find(&rows).Error)
			if len(rows) != 2 {
				t.Fatalf("retry produced %d rows", len(rows))
			}
			for _, row := range rows {
				var got Observation
				must(t, json.Unmarshal(row.PayloadJSON, &got))
				if got.InventoryID == "" {
					if len(got.Candidates) != 1 || got.Candidates[0].ResourceID != "/outside" {
						t.Fatal("outside membership changed")
					}
				} else if len(got.Candidates) != 206 || got.Candidates[0].ResourceID != "/file/0" || got.Candidates[205].ResourceID != "/file/0" {
					t.Fatal("inside ordering or duplicates changed")
				}
			}
		})
	}
}

func TestFolderCollectionClassificationAndRetry(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	result, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_begin"}, FolderRequest{Source: encoded(map[string]string{"provider": "local", "namespace": "host-a", "root": "/fixture"}), State: encoded(map[string]any{"node_format_version": 2, "queue": []any{}, "errors": []any{}})})
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
	_, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_checkpoint"}, FolderRequest{InventoryID: inv.ID, Version: 1, Status: "complete", State: encoded(map[string]any{"node_format_version": 2, "queue": []any{}, "errors": []any{}}), Nodes: nodes})
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
		v, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_begin"}, FolderRequest{Source: encoded(map[string]string{"provider": "local", "root": "/empty"}), State: encoded(map[string]any{"node_format_version": 2})})
		must(t, err)
		inv := v["inventory"].(orm.DocumentFolderInventory)
		_, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_checkpoint"}, FolderRequest{InventoryID: inv.ID, Version: 1, Status: "complete", State: encoded(map[string]any{"node_format_version": 2, "queue": []any{}, "errors": []any{}}), Nodes: []orm.DocumentFolderNode{{NodeKey: "root", Kind: "directory", Listed: true, Name: "empty"}}})
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

func TestGoogleDriveReadIdentityAcrossFolderPipeline(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/document_directory_drive.json")
	must(t, err)
	var fixture struct {
		Source map[string]any         `json:"source"`
		Node   orm.DocumentFolderNode `json:"node"`
		Cases  []struct {
			Path        string      `json:"path"`
			Observation Observation `json:"observation"`
		} `json:"cases"`
	}
	must(t, json.Unmarshal(raw, &fixture))
	var enumerated Candidate
	must(t, json.Unmarshal(fixture.Node.CandidateJSON, &enumerated))
	fixture.Source["root"] = "FOLDER_789"
	for _, tc := range fixture.Cases {
		t.Run(tc.Path, func(t *testing.T) {
			db := testDB(t)
			ctx := context.Background()
			result, err := folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_begin"}, FolderRequest{Source: encoded(fixture.Source), State: encoded(map[string]any{"node_format_version": 2})})
			must(t, err)
			inv := result["inventory"].(orm.DocumentFolderInventory)
			nodes := []orm.DocumentFolderNode{{NodeKey: "root", Kind: "directory", Listed: true, Name: "root"}, fixture.Node}
			_, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_checkpoint"}, FolderRequest{InventoryID: inv.ID, Version: 1, Status: "complete", State: encoded(map[string]any{"node_format_version": 2, "queue": []any{}, "errors": []any{}}), Nodes: nodes})
			must(t, err)
			result, err = folderOperation(ctx, db, "u", "conv", Command{Operation: "folder_submit", Name: "shared drive contract"}, FolderRequest{InventoryID: inv.ID})
			must(t, err)
			var b orm.DocumentDirectoryBatch
			must(t, db.Where("id=?", result["batch_id"]).Take(&b).Error)
			var input Input
			must(t, json.Unmarshal(b.InputJSON, &input))
			until := time.Now().UTC().Add(time.Minute)
			must(t, db.Model(&orm.AsyncJob{}).Where("id=?", b.JobID).Updates(map[string]any{"status": "running", "attempt_count": 1, "lock_until": until}).Error)
			j := asyncjob.Job{ID: b.JobID, JobType: OrganizeJob, CreateUserID: "u", AttemptCount: 1}
			must(t, collectFolder(ctx, db, j, &b, input))
			obs := tc.Observation
			obs.InventoryID = inv.ID
			must(t, SaveObservations(ctx, db, "u", "conv", "history", "run", "Read this document", []Observation{obs}))
			var rows []orm.DocumentObservation
			must(t, db.Find(&rows).Error)
			inside, outside := 0, 0
			for _, row := range rows {
				var o Observation
				must(t, json.Unmarshal(row.PayloadJSON, &o))
				if o.InventoryID == inv.ID {
					inside += len(o.Candidates)
				} else {
					outside += len(o.Candidates)
				}
			}
			modelRead := ""
			must(t, organizeFolder(ctx, db, j, &b, input, func(_ string, data any) (Proposal, error) {
				resources := data.(map[string]any)["resources"].(map[string]Candidate)
				assignments := []Assignment{}
				for k, c := range resources {
					if c.ResourceID == "FILE_123" {
						modelRead = c.ReadState
					}
					assignments = append(assignments, Assignment{ResourceKey: k, Reason: "Keep the inventory member unclassified", EvidenceBasis: "metadata"})
				}
				return Proposal{Groups: []orm.DocumentDirectoryGroup{}, Assignments: assignments}, nil
			}))
			var entries []orm.DocumentDirectoryEntry
			must(t, db.Where("directory_id=?", b.DirectoryID).Find(&entries).Error)
			if len(entries) != 1 {
				t.Fatalf("entry count=%d", len(entries))
			}
			if entries[0].ResourceKey != resourceKey(enumerated) || entries[0].ContentVersion != enumerated.ContentVersion {
				t.Fatal("resource identity or raw content version drifted")
			}
			wanted := "read"
			if entries[0].ReadState != wanted || modelRead != wanted {
				t.Fatalf("entry=%s organizer=%s expected=%s", entries[0].ReadState, modelRead, wanted)
			}
			if inside != 1 || outside != 0 {
				t.Fatalf("inside=%d outside=%d", inside, outside)
			}
			t.Logf("path=%s entry_id=%s entry_read=%s organizer_read=%s inside=%d outside=%d entries=%d", tc.Path, entries[0].ResourceID, entries[0].ReadState, modelRead, inside, outside, len(entries))
		})
	}
}

func TestEntryPageLiteralSearchAndPagination(t *testing.T) {
	db := testDB(t)
	d := create(t, db, "literal search")
	for i, name := range []string{"data-loader-0.md", "data-loader-1.md", "data-loader-2.md", "data-loader-3.md", "data-loader-4.md", "data_loader.md", "100% complete", "1000 complete", "Hello!", "HelloX", "literal!_%"} {
		c := sample("/" + name)
		c.Title = name
		if name == "literal!_%" {
			c.Title = "Resource identity only"
		}
		e := command(t, db, Command{Operation: "entry", DirectoryID: d.ID, Candidate: &c})["entry"].(orm.DocumentDirectoryEntry)
		must(t, db.Model(&e).Update("created_at", time.Unix(1000+int64(i), 0)).Error)
	}
	for _, tc := range []struct {
		query                string
		offset, total, count int
		first                string
	}{
		{" data_LOADER ", 0, 1, 1, "data_loader.md"}, {"data_loader", 5, 1, 0, ""},
		{"data", 0, 6, 5, "data-loader-0.md"}, {"data", 5, 6, 1, "data_loader.md"},
		{"100%", 0, 1, 1, "100% complete"}, {"Hello!", 0, 1, 1, "Hello!"},
		{"!_%", 0, 1, 1, "Resource identity only"}, {"  ", 10, 11, 1, "Resource identity only"},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.query, tc.offset), func(t *testing.T) {
			page, err := Get(context.Background(), db, "u", d.ID, EntryPageQuery{Limit: 5, Offset: tc.offset, Query: tc.query})
			must(t, err)
			if page.Total != int64(tc.total) || len(page.Entries) != tc.count {
				t.Fatalf("total=%d entries=%d", page.Total, len(page.Entries))
			}
			if tc.count > 0 && page.Entries[0].Title != tc.first {
				t.Fatalf("first=%q", page.Entries[0].Title)
			}
		})
	}
}

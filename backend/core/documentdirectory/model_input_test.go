package documentdirectory

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIncrementalPayloadDoesNotIncludeUnrelatedHistory(t *testing.T) {
	for _, n := range []int{100, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := testDB(t)
			store.Init(db, nil, nil)
			d := create(t, db, "payload")
			entries := make([]orm.DocumentDirectoryEntry, 0, n)
			for i := 0; i < n; i++ {
				entries = append(entries, entryFromCandidate(d.ID, sample(fmt.Sprintf("/historical/%04d", i)), time.Now().UTC()))
			}
			must(t, db.CreateInBatches(&entries, 50).Error)
			c := sample("/one-new-resource")
			input := Input{Observations: []orm.DocumentObservation{{PayloadJSON: encoded(Observation{Candidates: []Candidate{c}})}}}
			b, j := batch(t, db, d, input)
			j.PayloadJSON = encoded(map[string]string{"batch_id": b.ID})
			type measured struct{ Bytes, Entries, Resources int }
			seen := make(chan measured, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var request struct {
					Input struct {
						Entries   []json.RawMessage          `json:"entries"`
						Resources map[string]json.RawMessage `json:"resources"`
					} `json:"input"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Error(err)
					return
				}
				seen <- measured{len(body), len(request.Input.Entries), len(request.Input.Resources)}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(modelResponse{Status: "succeeded", Output: Proposal{Assignments: []Assignment{}}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			old := LoadChatModel
			LoadChatModel = func(context.Context, *gorm.DB, string, string) (map[string]any, error) { return map[string]any{}, nil }
			defer func() { LoadChatModel = old }()
			_, err := handleJob(context.Background(), j, nil)
			must(t, err)
			got := <-seen
			if got.Entries > 20 || got.Bytes > 65536 || got.Resources != 1 {
				t.Fatalf("unbounded input: %+v", got)
			}
			t.Logf("history=%d new_resources=%d transmitted_entries=%d request_bytes=%d", n, got.Resources, got.Entries, got.Bytes)
		})
	}
}

func TestFolderEvidenceLoadedOncePerExecution(t *testing.T) {
	db := testDB(t)
	d := create(t, db, "folder-metrics")
	ctx := context.Background()
	input := Input{Manual: true, FolderInventoryID: "metrics-inventory"}
	b, j := batch(t, db, d, input)
	must(t, db.Create(&orm.DocumentFolderInventory{ID: input.FolderInventoryID, UserID: "u", ConversationID: "conv", Status: "complete", DirectoryID: d.ID, BatchID: b.ID, SourceJSON: encoded(map[string]any{"provider": "local", "namespace": "host-a"}), StateJSON: encoded(map[string]any{})}).Error)
	for i := 0; i < 61; i++ {
		c := sample(fmt.Sprintf("/metric/%02d", i))
		e := entryFromCandidate(d.ID, c, time.Now().UTC())
		must(t, db.Create(&e).Error)
		o := orm.DocumentObservation{ID: fmt.Sprintf("metric-%d", i), UserID: "u", ConversationID: "conv", HistoryID: "h", RunID: "r", CallID: fmt.Sprint(i), BatchID: b.ID, PayloadJSON: encoded(Observation{Candidates: []Candidate{c}}), CreatedAt: time.Now().UTC()}
		must(t, db.Create(&o).Error)
	}
	loads := []int64{}
	must(t, db.Callback().Query().After("gorm:query").Register("review_evidence_loads", func(tx *gorm.DB) {
		if tx.Statement.Table == "document_observations" {
			loads = append(loads, tx.RowsAffected)
		}
	}))
	chunks := []int{}
	must(t, organizeFolder(ctx, db, j, &b, input, func(mode string, data any) (Proposal, error) {
		chunks = append(chunks, len(data.(map[string]any)["resources"].(map[string]Candidate)))
		return Proposal{Assignments: []Assignment{}}, nil
	}))
	if len(loads) != 1 || loads[0] != 61 {
		t.Fatalf("evidence queries: %v", loads)
	}
	t.Logf("input_observations=61 model_chunk_sizes=%v observation_rows_read_per_chunk=%v", chunks, loads)
}

func TestReorganizationIsHiddenAndRetainsOmittedResourcesAcrossRetry(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(fmt.Sprint(folder), func(t *testing.T) { testReorganizationResume(t, folder) })
	}
}

func testReorganizationResume(t *testing.T, folder bool) {
	db := testDB(t)
	oldDB, oldLazy, oldState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, nil, nil)
	defer store.Init(oldDB, oldLazy, oldState)
	d := create(t, db, "original")
	for i := 0; i < 61; i++ {
		e := entryFromCandidate(d.ID, sample(fmt.Sprintf("/selected/%03d", i)), time.Now().UTC())
		must(t, db.Create(&e).Error)
	}
	if folder {
		must(t, db.Create(&orm.DocumentFolderInventory{ID: "original-folder", UserID: "u", ConversationID: "conv", DirectoryID: d.ID, Status: "complete", SourceJSON: encoded(map[string]string{"provider": "local", "namespace": "host-a", "root": "/selected"}), StateJSON: encoded(map[string]any{"node_format_version": 2})}).Error)
		var entries []orm.DocumentDirectoryEntry
		must(t, db.Where("directory_id=?", d.ID).Find(&entries).Error)
		for _, e := range entries {
			must(t, db.Create(&orm.DocumentFolderNode{ID: e.ID, InventoryID: "original-folder", NodeKey: e.ResourceKey, ResourceKey: e.ResourceKey, CandidateJSON: encoded(sample(e.ResourceID)), Kind: "file"}).Error)
		}
	}
	result := command(t, db, Command{Operation: "reorganize", DirectoryID: d.ID, Organization: "by type", Name: "replacement"})
	b := result["batch"].(orm.DocumentDirectoryBatch)
	calls := 0
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request modelRequest
		must(t, json.NewDecoder(r.Body).Decode(&request))
		calls++
		rows, err := List(context.Background(), db, "u")
		must(t, err)
		for _, row := range rows {
			if row.ID == b.DirectoryID {
				t.Error("partial replacement became visible")
			}
		}
		if calls == 2 {
			w.WriteHeader(503)
			return
		}
		values := request.Input.(map[string]any)["resources"].(map[string]any)
		if len(values) > 30 || len(encoded(request.Input)) > 65536 {
			t.Error("chunk exceeds budget")
		}
		for k := range values {
			seen[k]++
		}
		_ = json.NewEncoder(w).Encode(modelResponse{Status: "succeeded", Usage: map[string]any{"total_tokens": 10}, Output: Proposal{Assignments: []Assignment{}}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	old := LoadChatModel
	LoadChatModel = func(context.Context, *gorm.DB, string, string) (map[string]any, error) { return map[string]any{}, nil }
	defer func() { LoadChatModel = old }()
	run := func() error {
		until := time.Now().Add(time.Minute)
		must(t, db.Model(&orm.AsyncJob{}).Where("id=?", b.JobID).Updates(map[string]any{"status": "running", "attempt_count": 1, "lock_until": until}).Error)
		_, err := handleJob(context.Background(), asyncjob.Job{ID: b.JobID, CreateUserID: "u", JobType: OrganizeJob, AttemptCount: 1, PayloadJSON: encoded(map[string]string{"batch_id": b.ID})}, nil)
		return err
	}
	if err := run(); err == nil {
		t.Fatal("expected second chunk to fail")
	}
	must(t, db.Where("id=?", b.ID).Take(&b).Error)
	var progress map[string]any
	must(t, json.Unmarshal(b.ResultJSON, &progress))
	if progress["processed"] != float64(30) {
		t.Fatalf("progress=%v", progress)
	}
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", b.JobID).Updates(map[string]any{"status": "failed", "lock_until": nil}).Error)
	result = command(t, db, Command{Operation: "retry", BatchID: b.ID})
	b = result["batch"].(orm.DocumentDirectoryBatch)
	must(t, run())
	if len(seen) != 61 {
		t.Fatalf("retained %d resources", len(seen))
	}
	for _, n := range seen {
		if n != 1 {
			t.Fatal("replayed committed chunk")
		}
	}
	for _, id := range []string{d.ID, b.DirectoryID} {
		view, err := Get(context.Background(), db, "u", id)
		must(t, err)
		if len(view.Entries) != 61 || !view.Directory.Ready {
			t.Fatalf("invalid published view: %+v", view)
		}
	}
	must(t, db.Where("id=?", b.ID).Take(&b).Error)
	must(t, json.Unmarshal(b.ResultJSON, &progress))
	if progress["usage"].(map[string]any)["total_tokens"] != float64(30) {
		t.Fatalf("lost cumulative usage: %v", progress)
	}
}

func TestOrganizerShrinksEvidenceChunksAndRejectsIndivisibleContext(t *testing.T) {
	d := orm.DocumentDirectory{ID: "d", Intent: "contracts", Organization: "types"}
	input := Input{}
	keys := []string{}
	for i := 0; i < 30; i++ {
		c := sample(fmt.Sprint("/", i))
		c.Excerpt = strings.Repeat("语", 2000)
		keys = append(keys, resourceKey(c))
		input.Observations = append(input.Observations, orm.DocumentObservation{ID: fmt.Sprint(i), PayloadJSON: encoded(Observation{Candidates: []Candidate{c}})})
	}
	_, payload, n, err := boundedOrganizationChunk(d, nil, nil, input, keys)
	must(t, err)
	if n < 1 || n >= 30 || len(encoded(payload)) > 65536 {
		t.Fatalf("unbounded multibyte input %d / %d", n, len(encoded(payload)))
	}
	d.Intent = strings.Repeat("x", 65536)
	if _, _, _, err = boundedOrganizationChunk(d, nil, nil, input, keys[:1]); err == nil {
		t.Fatal("oversize essential context silently truncated")
	}
}

func TestRoutingAlsoBoundsInputAndCoalescesChunkTargets(t *testing.T) {
	db := testDB(t)
	oldDB, oldLazy, oldState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, nil, nil)
	defer store.Init(oldDB, oldLazy, oldState)
	d := create(t, db, "target")
	input := Input{}
	for i := 0; i < 61; i++ {
		c := sample(fmt.Sprint("/route/", i))
		input.Observations = append(input.Observations, orm.DocumentObservation{ID: fmt.Sprint(i), PayloadJSON: encoded(Observation{Candidates: []Candidate{c}})})
	}
	b, j := batch(t, db, d, input)
	j.JobType = PlanJob
	j.PayloadJSON = encoded(map[string]string{"batch_id": b.ID})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request modelRequest
		must(t, json.NewDecoder(r.Body).Decode(&request))
		calls++
		resources := request.Input.(map[string]any)["resources"].(map[string]any)
		if len(resources) > 30 || len(encoded(request.Input)) > 65536 {
			t.Errorf("oversize routing input: %d resources", len(resources))
		}
		keys := []string{}
		for k := range resources {
			keys = append(keys, k)
		}
		_ = json.NewEncoder(w).Encode(modelResponse{Status: "succeeded", Output: Proposal{Targets: []Target{{DirectoryID: d.ID, ResourceKeys: keys}}}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	old := LoadChatModel
	LoadChatModel = func(context.Context, *gorm.DB, string, string) (map[string]any, error) { return map[string]any{}, nil }
	defer func() { LoadChatModel = old }()
	_, err := handleJob(context.Background(), j, nil)
	must(t, err)
	if calls != 3 {
		t.Fatalf("routing calls %d", calls)
	}
	var children []orm.DocumentDirectoryBatch
	must(t, db.Where("parent_id=?", b.ID).Find(&children).Error)
	if len(children) != 1 {
		t.Fatal("split target into duplicate jobs", len(children))
	}
	var child Input
	must(t, json.Unmarshal(children[0].InputJSON, &child))
	if len(candidates(child)) != 61 {
		t.Fatal("routing dropped resources")
	}
}

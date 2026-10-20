package documentdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func testDB(t *testing.T) *gorm.DB {
	return orm.MigrateTestDB(t, &orm.DocumentFolderInventory{}, &orm.DocumentFolderNode{}, &orm.DocumentDirectory{}, &orm.DocumentDirectoryGroup{}, &orm.DocumentDirectoryEntry{}, &orm.DocumentObservation{}, &orm.DocumentDirectoryBatch{}, &orm.AsyncJob{}, &orm.ConversationIdleEvent{}).DB
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func command(t *testing.T, db *gorm.DB, c Command) map[string]any {
	t.Helper()
	r, e := Execute(context.Background(), db, "u", c)
	must(t, e)
	return r
}
func create(t *testing.T, db *gorm.DB, name string) orm.DocumentDirectory {
	return command(t, db, Command{Operation: "create", Name: name, Intent: name})["directory"].(orm.DocumentDirectory)
}
func sample(id string) Candidate {
	return Candidate{Source: "local", Namespace: "host-a", ResourceID: id, Title: id, Locator: encoded(map[string]string{"path": id}), ReadState: "unread", AccessState: "unknown"}
}
func batch(t *testing.T, db *gorm.DB, d orm.DocumentDirectory, input Input) (orm.DocumentDirectoryBatch, asyncjob.Job) {
	t.Helper()
	now := time.Now().UTC()
	b := orm.DocumentDirectoryBatch{ID: fmt.Sprintf("batch-%d", now.UnixNano()), UserID: "u", OwnerKey: "u", TriggerID: fmt.Sprint(now.UnixNano()), DirectoryID: d.ID, InputJSON: encoded(input), CreatedAt: now, Status: "pending"}
	must(t, enqueue(context.Background(), db, &b, OrganizeJob, d.ID))
	until := now.Add(time.Minute)
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", b.JobID).Updates(map[string]any{"status": "running", "attempt_count": 1, "lock_until": until}).Error)
	return b, asyncjob.Job{ID: b.JobID, JobType: OrganizeJob, CreateUserID: "u", AttemptCount: 1}
}
func finish(t *testing.T, db *gorm.DB, j asyncjob.Job) {
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", j.ID).Updates(map[string]any{"status": "succeeded", "lock_until": nil}).Error)
}
func TestNewDirectoryOrganization(t *testing.T) {
	for _, organization := range []string{"", "按归档年份组织"} {
		t.Run(organization, func(t *testing.T) {
			db := testDB(t)
			ctx := context.Background()
			want := organization
			if want == "" {
				want = "按资料类型组织"
			}
			manual := command(t, db, Command{Operation: "create", Name: "manual", Intent: "manual", Organization: organization})["directory"].(orm.DocumentDirectory)
			stored, err := requireDirectory(db, "u", manual.ID)
			must(t, err)
			if stored.Organization != want {
				t.Fatalf("manual organization = %q, want %q", stored.Organization, want)
			}
			candidate := sample("/plan")
			input := Input{Observations: []orm.DocumentObservation{{ID: "observation", CreatedAt: time.Now().UTC(), PayloadJSON: encoded(Observation{CallID: "search", Source: "local", Status: "success", Candidates: []Candidate{candidate}})}}}
			root, job := batch(t, db, manual, input)
			job.JobType = PlanJob
			must(t, db.Model(&orm.AsyncJob{}).Where("id=?", job.ID).Updates(map[string]any{"job_type": PlanJob, "resource_id": "u"}).Error)
			root.DirectoryID = ""
			must(t, db.Save(&root).Error)
			must(t, applyTargets(ctx, db, job, &root, input, Proposal{Targets: []Target{{Name: "automatic", Intent: "automatic", Organization: organization, ResourceKeys: []string{resourceKey(candidate)}}}}))
			var automatic orm.DocumentDirectory
			must(t, db.Where("user_id=? AND intent=?", "u", "automatic").Take(&automatic).Error)
			if automatic.Organization != want {
				t.Fatalf("automatic organization = %q, want %q", automatic.Organization, want)
			}
		})
	}
}
func TestPlacementExclusionAndAtomicCommit(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	d := create(t, db, "A")
	c := sample("/a")
	g := command(t, db, Command{Operation: "group", DirectoryID: d.ID, Name: "manual"})["group"].(orm.DocumentDirectoryGroup)
	e := command(t, db, Command{Operation: "entry", DirectoryID: d.ID, Candidate: &c, GroupID: g.ID})["entry"].(orm.DocumentDirectoryEntry)
	if !e.Pinned || !e.Confirmed {
		t.Fatal("manual placement not protected")
	}
	detail, err := Get(ctx, db, "u", d.ID)
	must(t, err)
	d = detail.Directory
	input := Input{Entries: []orm.DocumentDirectoryEntry{e}}
	b, j := batch(t, db, d, input)
	_, err = Execute(ctx, db, "u", Command{Operation: "update", DirectoryID: d.ID, Name: "changed"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("edit during organization: %v", err)
	}
	p := Proposal{Groups: []orm.DocumentDirectoryGroup{{ID: "new", Name: "auto"}}, Assignments: []Assignment{{ResourceKey: e.ResourceKey, GroupID: "new", Confirmed: true}}}
	must(t, applyOrganization(ctx, db, j, &b, d.Version, input, p))
	must(t, applyOrganization(ctx, db, j, &b, d.Version, input, p))
	finish(t, db, j)
	detail, err = Get(ctx, db, "u", d.ID)
	must(t, err)
	if len(detail.Groups) != 1 || len(detail.Entries) != 1 || detail.Entries[0].GroupID != g.ID {
		t.Fatalf("pinned position changed: %+v", detail)
	}
	off := false
	command(t, db, Command{Operation: "entry_pin", DirectoryID: d.ID, EntryID: e.ID, Pinned: &off})
	detail, err = Get(ctx, db, "u", d.ID)
	must(t, err)
	if detail.Entries[0].GroupID != g.ID {
		t.Fatal("unpin moved entry")
	}
	command(t, db, Command{Operation: "exclude", DirectoryID: d.ID, EntryID: e.ID})
	detail, err = Get(ctx, db, "u", d.ID)
	must(t, err)
	b, j = batch(t, db, detail.Directory, input)
	must(t, applyOrganization(ctx, db, j, &b, detail.Directory.Version, input, p))
	finish(t, db, j)
	detail, err = Get(ctx, db, "u", d.ID)
	must(t, err)
	if !detail.Entries[0].Excluded || len(detail.Groups) != 1 {
		t.Fatal("excluded entry restored or empty group created")
	}
	command(t, db, Command{Operation: "restore", DirectoryID: d.ID, EntryID: e.ID})
	detail, err = Get(ctx, db, "u", d.ID)
	must(t, err)
	b, j = batch(t, db, detail.Directory, input)
	bad := p
	bad.Assignments = append(append([]Assignment{}, p.Assignments...), Assignment{ResourceKey: "invented"})
	if err = applyOrganization(ctx, db, j, &b, detail.Directory.Version, input, bad); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid proposal: %v", err)
	}
	unchanged, err := Get(ctx, db, "u", d.ID)
	must(t, err)
	if unchanged.Directory.Version != detail.Directory.Version || len(unchanged.Groups) != 1 {
		t.Fatal("partial commit")
	}
	must(t, applyOrganization(ctx, db, j, &b, detail.Directory.Version, input, p))
	finish(t, db, j)
	detail, err = Get(ctx, db, "u", d.ID)
	must(t, err)
	if detail.Entries[0].GroupID == g.ID || detail.Entries[0].Confirmed {
		t.Fatal("restored unread entry did not move or falsely confirmed")
	}
}
func TestCapacityPinsAndExpiry(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	on := true
	dirs := []orm.DocumentDirectory{}
	for i := 0; i < 20; i++ {
		d := create(t, db, fmt.Sprint(i))
		dirs = append(dirs, d)
		must(t, db.Model(&d).Update("last_used_at", time.Now().UTC().Add(time.Duration(i-30)*time.Hour)).Error)
		if i < 10 {
			command(t, db, Command{Operation: "update", DirectoryID: d.ID, Pinned: &on})
		}
	}
	if _, e := Execute(ctx, db, "u", Command{Operation: "update", DirectoryID: dirs[10].ID, Pinned: &on}); !errors.Is(e, ErrPinLimit) {
		t.Fatalf("pin cap: %v", e)
	}
	r := command(t, db, Command{Operation: "create", Name: "new", Intent: "new"})
	if r["evicted"] != "10" {
		t.Fatalf("wrong eviction %v", r)
	}
	list, e := List(ctx, db, "u")
	must(t, e)
	if len(list) != 20 {
		t.Fatal(len(list))
	}
	until := time.Now().UTC().Add(time.Hour)
	must(t, db.Model(&orm.DocumentDirectory{}).Where("user_id=? AND pinned=?", "u", false).Update("use_until", until).Error)
	if _, e = Execute(ctx, db, "u", Command{Operation: "create", Name: "busy", Intent: "busy"}); !errors.Is(e, ErrCapacity) {
		t.Fatalf("busy capacity: %v", e)
	}
	must(t, db.Model(&orm.DocumentDirectory{}).Where("user_id=?", "u").Updates(map[string]any{"use_until": nil, "last_used_at": time.Now().UTC().Add(-8 * 24 * time.Hour)}).Error)
	must(t, Cleanup(ctx, db))
	list, e = List(ctx, db, "u")
	must(t, e)
	if len(list) != 10 {
		t.Fatal("expiry removed pins or retained expired views", len(list))
	}
}
func TestIdleFreezesDurableInputsAndScopesIdentity(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	a := sample("/same")
	b := a
	b.Namespace = "host-b"
	if resourceKey(a) == resourceKey(b) {
		t.Fatal("cross-account identity collision")
	}
	obs := []Observation{{CallID: "call", Source: "local", Operation: "search", Status: "success", Scope: encoded(map[string]string{"query": "A"}), Candidates: []Candidate{a, b}}}
	must(t, SaveObservations(ctx, db, "u", "conv", "history", "run", "A", obs))
	must(t, SaveObservations(ctx, db, "u", "conv", "history", "run", "A", obs))
	event := orm.ConversationIdleEvent{ID: "event", EventID: "event", UserID: "u", ConversationID: "conv", Status: "waiting", LastActivityAt: time.Now().UTC(), DueAt: time.Now().UTC().Add(-time.Minute)}
	must(t, db.Create(&event).Error)
	must(t, QueueIdle(ctx, db, event))
	must(t, QueueIdle(ctx, db, event))
	var batches []orm.DocumentDirectoryBatch
	must(t, db.Find(&batches).Error)
	if len(batches) != 1 {
		t.Fatal("duplicate batch", len(batches))
	}
	obs[0].CallID = "later"
	must(t, SaveObservations(ctx, db, "u", "conv", "history2", "run2", "B", obs))
	var count int64
	must(t, db.Model(&orm.DocumentObservation{}).Where("batch_id=?", "").Count(&count).Error)
	if count != 1 {
		t.Fatal("later evidence not left for next batch")
	}
}
func TestStaleExecutionCannotCommitAndDeletionCancelsInput(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	d := create(t, db, "A")
	c := sample("/a")
	e := entryFromCandidate(d.ID, c, time.Now().UTC())
	input := Input{Entries: []orm.DocumentDirectoryEntry{e}}
	b, j := batch(t, db, d, input)
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", j.ID).Update("lock_until", time.Now().Add(-time.Minute)).Error)
	p := Proposal{Assignments: []Assignment{{ResourceKey: e.ResourceKey}}}
	if err := applyOrganization(ctx, db, j, &b, d.Version, input, p); !errors.Is(err, maintenanceLeaseLost) {
		t.Fatalf("stale commit: %v", err)
	}
	command(t, db, Command{Operation: "delete", DirectoryID: d.ID})
	if err := applyOrganization(ctx, db, j, &b, d.Version, input, p); err == nil {
		t.Fatal("deleted directory resurrected")
	}
	if _, err := Execute(ctx, db, "u", Command{Operation: "retry", BatchID: b.ID}); err == nil {
		t.Fatal("deleted input retried")
	}
	if _, err := Get(ctx, db, "other", d.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("ownership")
	}
}
func TestMigrationUpgradeRollbackPreservesExistingData(t *testing.T) {
	db := orm.OpenTestDB(t).DB
	must(t, db.Exec("CREATE TABLE existing_document_test (id INTEGER PRIMARY KEY, title TEXT)").Error)
	must(t, db.Exec("INSERT INTO existing_document_test VALUES (1, 'keep')").Error)
	root := "../migrations/dev_mode/v0_3/20261010023408_document_directories."
	for _, direction := range []string{"up", "up", "down", "up"} {
		body, err := os.ReadFile(root + direction + ".sql")
		must(t, err)
		for _, sql := range strings.Split(string(body), ";") {
			if strings.TrimSpace(sql) != "" {
				must(t, db.Exec(sql).Error)
			}
		}
	}
	var title string
	must(t, db.Raw("SELECT title FROM existing_document_test WHERE id=1").Scan(&title).Error)
	if title != "keep" {
		t.Fatal("unrelated data lost")
	}
	must(t, db.AutoMigrate(&orm.DocumentFolderInventory{}, &orm.DocumentFolderNode{}))
	d := create(t, db, "migration")
	_, err := Get(context.Background(), db, "u", d.ID) // async_jobs is external to this migration
	if err != nil && !strings.Contains(err.Error(), "async_jobs") {
		t.Fatal(err)
	}
}

func TestMultiTargetBatchCreatesBAndUpdatesAWithoutReplayingDeletedInputs(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	a := create(t, db, "A")
	ca, cb := sample("/a"), sample("/b")
	o := orm.DocumentObservation{ID: "input", CreatedAt: time.Now().UTC(), Query: "compare A and B", PayloadJSON: encoded(Observation{CallID: "search", Source: "local", Status: "success", Candidates: []Candidate{ca, cb}})}
	input := Input{Observations: []orm.DocumentObservation{o}}
	root, j := batch(t, db, a, input)
	j.JobType = PlanJob
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", j.ID).Updates(map[string]any{"job_type": PlanJob, "resource_id": "u"}).Error)
	root.DirectoryID = ""
	must(t, db.Save(&root).Error)
	p := Proposal{Targets: []Target{{DirectoryID: a.ID, ResourceKeys: []string{resourceKey(ca)}}, {Name: "B", Intent: "B", ResourceKeys: []string{resourceKey(cb)}}}}
	must(t, applyTargets(ctx, db, j, &root, input, p))
	finish(t, db, j)
	var children []orm.DocumentDirectoryBatch
	must(t, db.Where("parent_id=?", root.ID).Find(&children).Error)
	if len(children) != 2 {
		t.Fatal(len(children))
	}
	list, err := List(ctx, db, "u")
	must(t, err)
	if len(list) != 1 {
		t.Fatal("uncommitted B visible")
	}
	for _, child := range children {
		var ci Input
		must(t, json.Unmarshal(child.InputJSON, &ci))
		d, err := requireDirectory(db, "u", child.DirectoryID)
		must(t, err)
		until := time.Now().Add(time.Minute)
		must(t, db.Model(&orm.AsyncJob{}).Where("id=?", child.JobID).Updates(map[string]any{"status": "running", "attempt_count": 1, "lock_until": until}).Error)
		job := asyncjob.Job{ID: child.JobID, JobType: OrganizeJob, CreateUserID: "u", AttemptCount: 1}
		assign := []Assignment{}
		for key := range candidates(ci) {
			assign = append(assign, Assignment{ResourceKey: key})
		}
		must(t, applyOrganization(ctx, db, job, &child, d.Version, ci, Proposal{Assignments: assign}))
		finish(t, db, job)
	}
	list, err = List(ctx, db, "u")
	must(t, err)
	if len(list) != 2 {
		t.Fatal("missing B")
	}
	command(t, db, Command{Operation: "delete", DirectoryID: a.ID})
	retry, j2 := batch(t, db, list[0], input)
	j2.JobType = PlanJob
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", j2.ID).Update("job_type", PlanJob).Error)
	must(t, applyTargets(ctx, db, j2, &retry, input, Proposal{Targets: []Target{{Name: "A again", Intent: "A", ResourceKeys: []string{resourceKey(ca)}}}}))
	finish(t, db, j2)
	var n int64
	must(t, db.Model(&orm.DocumentDirectory{}).Where("user_id=? AND deleted_at IS NULL", "u").Count(&n).Error)
	if n != 1 {
		t.Fatal("old evidence rebuilt deleted A")
	}
	input.Observations[0].CreatedAt = time.Now().Add(time.Second)
	future, j3 := batch(t, db, list[0], input)
	j3.JobType = PlanJob
	must(t, db.Model(&orm.AsyncJob{}).Where("id=?", j3.ID).Update("job_type", PlanJob).Error)
	must(t, applyTargets(ctx, db, j3, &future, input, Proposal{Targets: []Target{{Name: "A future", Intent: "A", ResourceKeys: []string{resourceKey(ca)}}}}))
	finish(t, db, j3)
	must(t, db.Model(&orm.DocumentDirectory{}).Where("user_id=? AND deleted_at IS NULL", "u").Count(&n).Error)
	if n != 2 {
		t.Fatal("future task could not create A")
	}
}

func TestCandidateMetadataSurvivesSearchReadAndCommit(t *testing.T) {
	db := testDB(t)
	d := create(t, db, "Metadata")
	searched := sample("node1")
	searched.Title = "Contract A"
	searched.Locator = encoded(map[string]any{"url": "https://example.test/wiki/node1"})
	read := sample("node1")
	now := time.Now().UTC()
	read.ReadAt, read.ReadState, read.AccessState, read.Excerpt = &now, "partial", "accessible", "terms"
	read.Locator = encoded(map[string]any{"path": "~node/node1", "offset": 10, "end_line": 20, "eof": false})
	input := Input{Observations: []orm.DocumentObservation{
		{PayloadJSON: encoded(Observation{Candidates: []Candidate{searched}})},
		{PayloadJSON: encoded(Observation{Candidates: []Candidate{read}})},
	}}
	c := candidates(input)[resourceKey(read)]
	if c.Title != searched.Title || c.Excerpt != "terms" || c.ReadState != "partial" || !strings.Contains(string(c.Locator), "https://example.test") {
		t.Fatalf("metadata lost: %+v", c)
	}
	e := command(t, db, Command{Operation: "entry", DirectoryID: d.ID, Candidate: &searched})["entry"].(orm.DocumentDirectoryEntry)
	detail, err := Get(context.Background(), db, "u", d.ID)
	must(t, err)
	b, j := batch(t, db, detail.Directory, input)
	must(t, applyOrganization(context.Background(), db, j, &b, detail.Directory.Version, input, Proposal{Assignments: []Assignment{{ResourceKey: resourceKey(c)}}}))
	finish(t, db, j)
	detail, err = Get(context.Background(), db, "u", d.ID)
	must(t, err)
	if len(detail.Entries) != 1 || detail.Entries[0].Title != "Contract A" || !detail.Entries[0].Pinned {
		t.Fatalf("commit lost metadata or pin: %+v", detail.Entries)
	}
	denied := sample("node1")
	denied.AccessState = "denied"
	merged := mergeCandidate(c, denied)
	if merged.ReadState != "partial" || merged.AccessState != "denied" {
		t.Fatal("access and read state conflated")
	}
	complete := read
	later := now.Add(time.Second)
	complete.ReadAt = &later
	complete.ReadState = "read"
	complete.Locator = encoded(map[string]any{"path": "~node/node1"})
	merged = mergeCandidate(merged, complete)
	if strings.Contains(string(merged.Locator), "offset") || merged.ReadState != "read" {
		t.Fatal("old read window retained")
	}
	command(t, db, Command{Operation: "exclude", DirectoryID: d.ID, EntryID: e.ID})
	detail, err = Get(context.Background(), db, "u", d.ID)
	must(t, err)
	if len(organizerResources(input, detail.Entries)) != 0 {
		t.Fatal("excluded resource sent to model")
	}
	command(t, db, Command{Operation: "organize", DirectoryID: d.ID})
	var manual orm.DocumentDirectoryBatch
	must(t, db.Where("directory_id=? AND status=?", d.ID, "pending").Take(&manual).Error)
	var frozen Input
	must(t, json.Unmarshal(manual.InputJSON, &frozen))
	if len(candidates(frozen)) != 0 {
		t.Fatal("manual input contains excluded resource")
	}
	b, j = batch(t, db, detail.Directory, frozen)
	must(t, applyOrganization(context.Background(), db, j, &b, detail.Directory.Version, frozen, Proposal{Groups: []orm.DocumentDirectoryGroup{}, Assignments: []Assignment{}}))
	detail, err = Get(context.Background(), db, "u", d.ID)
	must(t, err)
	if !detail.Entries[0].Excluded {
		t.Fatal("empty organization restored excluded entry")
	}
}

func TestConfirmationUsesBodyEvidenceAndPreservesPriorDecision(t *testing.T) {
	for _, tc := range []struct {
		name                                                       string
		confirmed, body, newer, changedVersion, moved, model, want bool
	}{
		{"partial_body", false, true, true, false, false, true, true},
		{"unread_title", false, false, false, false, false, true, false},
		{"retain_without_body", true, false, false, false, false, false, true},
		{"retain_old_read", true, true, false, false, false, false, true},
		{"new_ambiguous_body", true, true, true, false, false, false, false},
		{"version_changed", true, false, false, true, false, true, false},
		{"version_changed_old_body", true, true, false, true, false, true, false},
		{"version_changed_new_body", true, true, true, true, false, true, true},
		{"group_changed", true, false, false, false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			ctx := context.Background()
			d := create(t, db, tc.name)
			old := time.Now().UTC().Add(-time.Hour)
			c := sample("/plan")
			c.ContentVersion = "version:1"
			e := entryFromCandidate(d.ID, c, old)
			e.Confirmed = tc.confirmed
			if tc.confirmed {
				e.ReadAt = &old
				e.ReadState = "partial"
			}
			e.EvidenceJSON = encoded(map[string]any{"reason": "prior body evidence"})
			must(t, db.Create(&e).Error)
			if tc.body {
				c.ReadAt = &old
				c.ReadState = "partial"
				c.Excerpt = "This is the project design plan"
				c.AccessState = "accessible"
			}
			if tc.newer {
				newer := old.Add(time.Minute)
				c.ReadAt = &newer
			}
			if tc.changedVersion {
				c.ContentVersion = "version:2"
			}
			input := Input{Observations: []orm.DocumentObservation{{PayloadJSON: encoded(Observation{Candidates: []Candidate{c}})}}}
			proposal := Proposal{Assignments: []Assignment{{ResourceKey: e.ResourceKey, Confirmed: tc.model, Reason: "new assessment"}}}
			if tc.moved {
				proposal.Groups = []orm.DocumentDirectoryGroup{{ID: "new", Name: "Plans"}}
				proposal.Assignments[0].GroupID = "new"
			}
			b, j := batch(t, db, d, input)
			must(t, applyOrganization(ctx, db, j, &b, d.Version, input, proposal))
			var result orm.DocumentDirectoryEntry
			must(t, db.First(&result, "id=?", e.ID).Error)
			if result.Confirmed != tc.want {
				t.Fatalf("confirmed=%v want %v", result.Confirmed, tc.want)
			}
			if tc.confirmed && !tc.newer && !tc.changedVersion && !tc.moved && string(result.EvidenceJSON) != string(e.EvidenceJSON) {
				t.Fatal("prior evidence overwritten")
			}
		})
	}
}

func TestMetadataConfirmationDoesNotRequireOrCreateReadEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, title, reason string
		model, want         bool
	}{
		{"clear_invitation", "alice invited you to alice/ProjectX", "Title identifies a ProjectX repository invitation, explicitly in directory scope", true, true},
		{"ambiguous_topic", "Repository invitation", "Title does not identify ProjectX", false, false},
		{"missing_title", "", "ProjectX invitation", true, false},
		{"missing_reason", "ProjectX invitation", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			d := create(t, db, tc.name)
			c := sample("invitation")
			c.Title = tc.title
			c.ContentVersion = "version:1"
			e := entryFromCandidate(d.ID, c, time.Now().UTC())
			if tc.name == "ambiguous_topic" {
				e.Title = "alice invited you to alice/ProjectX"
				e.Confirmed = true
			}
			must(t, db.Create(&e).Error)
			// A metadata decision uses the supplied title even if the body changed.
			if tc.name != "ambiguous_topic" {
				c.ContentVersion = "version:2"
			}
			input := Input{Observations: []orm.DocumentObservation{{PayloadJSON: encoded(Observation{Candidates: []Candidate{c}})}}}
			p := Proposal{Assignments: []Assignment{{ResourceKey: e.ResourceKey, Confirmed: tc.model, EvidenceBasis: "metadata", Reason: tc.reason}}}
			b, j := batch(t, db, d, input)
			must(t, applyOrganization(context.Background(), db, j, &b, d.Version, input, p))
			var result orm.DocumentDirectoryEntry
			must(t, db.First(&result, "id=?", e.ID).Error)
			if result.Confirmed != tc.want || result.ReadState != "unread" || result.ReadAt != nil {
				t.Fatalf("confirmation/read state mismatch: confirmed=%v read=%s at=%v", result.Confirmed, result.ReadState, result.ReadAt)
			}
			var evidence map[string]any
			must(t, json.Unmarshal(result.EvidenceJSON, &evidence))
			if evidence["evidence_basis"] != "metadata" || evidence["reason"] != tc.reason {
				t.Fatalf("missing decision evidence: %v", evidence)
			}
		})
	}
}

func TestReadWindowMergeKeepsPhysicalMetadataTogether(t *testing.T) {
	now := time.Now().UTC()
	old := sample("file")
	old.ReadAt = &now
	old.Locator = encoded(map[string]any{"path": "/a", "range_kind": "reader_logical_lines", "offset": 172, "end_line": 173})
	fresh := old
	later := now.Add(time.Second)
	fresh.ReadAt = &later
	fresh.Locator = encoded(map[string]any{"path": "/a", "range_kind": "physical_lines", "line_unit": "physical", "offset": 35, "end_line": 35, "truncated_lines": []int{35}, "next_offset": 36})
	merged := mergeCandidate(old, fresh)
	var locator map[string]any
	must(t, json.Unmarshal(merged.Locator, &locator))
	if locator["range_kind"] != "physical_lines" || locator["offset"] != float64(35) || locator["line_unit"] != "physical" || len(locator["truncated_lines"].([]any)) != 1 {
		t.Fatalf("mixed read window: %s", merged.Locator)
	}
	search := sample("file")
	search.Locator = encoded(map[string]any{"path": "/a", "line_unit": "wrong", "offset": 1})
	merged = mergeCandidate(merged, search)
	if !strings.Contains(string(merged.Locator), `"offset":35`) {
		t.Fatal("search replaced successful read range")
	}
	fresh.ReadAt = &later
	fresh.Locator = encoded(map[string]any{"path": "/a", "line_unit": "physical", "offset": 36, "end_line": 36, "eof": true})
	merged = mergeCandidate(merged, fresh)
	if strings.Contains(string(merged.Locator), "truncated_lines") || strings.Contains(string(merged.Locator), "next_offset") {
		t.Fatal("new read retained old clipping or cursor")
	}
}

func TestRestoreChecksOriginalGroupAndPreservesReading(t *testing.T) {
	for _, scenario := range []string{"existing", "deleted", "different_directory", "query_error"} {
		t.Run(scenario, func(t *testing.T) {
			db := testDB(t)
			d := create(t, db, "restore")
			g := command(t, db, Command{Operation: "group", DirectoryID: d.ID, Name: "G"})["group"].(orm.DocumentDirectoryGroup)
			c := sample("/read-document")
			c.ReadState, c.ContentVersion = "read", "version-1"
			now := time.Now().UTC()
			c.ReadAt, c.Excerpt = &now, "Verified document body"
			e := command(t, db, Command{Operation: "entry", DirectoryID: d.ID, GroupID: g.ID, Candidate: &c})["entry"].(orm.DocumentDirectoryEntry)
			if e.ReadState != "read" || !e.Pinned || !e.Confirmed {
				t.Fatal("restore fixture lacks reading evidence and manual placement")
			}
			command(t, db, Command{Operation: "exclude", DirectoryID: d.ID, EntryID: e.ID})
			if scenario == "deleted" {
				command(t, db, Command{Operation: "delete_group", DirectoryID: d.ID, GroupID: g.ID})
			} else if scenario == "different_directory" {
				other := create(t, db, "other")
				must(t, db.Model(&g).Update("directory_id", other.ID).Error)
			}
			before, err := requireDirectory(db, "u", d.ID)
			must(t, err)
			injected := errors.New("group lookup failed")
			if scenario == "query_error" {
				must(t, db.Callback().Query().Before("gorm:query").Register("fail_group_lookup", func(tx *gorm.DB) {
					if tx.Statement.Table == "document_directory_groups" {
						tx.AddError(injected)
					}
				}))
			}
			_, err = Execute(context.Background(), db, "u", Command{Operation: "restore", DirectoryID: d.ID, EntryID: e.ID})
			if scenario == "query_error" {
				must(t, db.Callback().Query().Remove("fail_group_lookup"))
				if !errors.Is(err, injected) {
					t.Fatalf("wanted lookup failure, got %v", err)
				}
			} else {
				must(t, err)
			}
			var restored orm.DocumentDirectoryEntry
			must(t, db.Where("id=?", e.ID).Take(&restored).Error)
			if restored.ResourceKey != e.ResourceKey || restored.ReadState != e.ReadState || restored.Pinned != e.Pinned {
				t.Fatalf("restore changed identity, read evidence or pin: %+v", restored)
			}
			if scenario == "query_error" {
				after, err := requireDirectory(db, "u", d.ID)
				must(t, err)
				if !restored.Excluded || restored.GroupID != g.ID || after.Version != before.Version {
					t.Fatal("failed restore was not rolled back")
				}
				return
			}
			if restored.Excluded {
				t.Fatal("entry not restored")
			}
			group := g.ID
			if scenario != "existing" {
				group = ""
				var evidence map[string]any
				must(t, json.Unmarshal(restored.EvidenceJSON, &evidence))
				if restored.Confirmed || len(evidence) != 0 {
					t.Fatal("orphan grouping evidence survived restore")
				}
			} else if restored.Confirmed != e.Confirmed || string(restored.EvidenceJSON) != string(e.EvidenceJSON) {
				t.Fatal("valid grouping evidence changed")
			}
			if restored.GroupID != group {
				t.Fatalf("group=%q, want %q", restored.GroupID, group)
			}
			page, err := Get(context.Background(), db, "u", d.ID, EntryPageQuery{Limit: 5, Group: &group})
			must(t, err)
			if page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].ID != e.ID {
				t.Fatal("restored entry is not visible in its group")
			}
		})
	}
}

func TestFeaturePauseStopsWritesAndPreservesDirectories(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	must(t, db.AutoMigrate(&orm.UserUIPreferences{}))
	d := create(t, db, "keep")
	b, j := batch(t, db, d, Input{})
	must(t, SaveObservations(ctx, db, "u", "c", "h", "r", "query", []Observation{{CallID: "capture", Candidates: []Candidate{sample("/a")}}}))
	must(t, db.Model(&orm.DocumentDirectory{}).Where("id=?", d.ID).Update("last_used_at", time.Now().Add(-8*24*time.Hour)).Error)
	must(t, db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "u", "document_directories_enabled": false, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error)
	must(t, db.Transaction(func(tx *gorm.DB) error { return Pause(tx, "u") }))
	if _, err := Execute(ctx, db, "u", Command{Operation: "organize", DirectoryID: d.ID}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("manual: %v", err)
	}
	if err := SaveObservations(ctx, db, "u", "c", "h", "r", "", []Observation{{CallID: "later"}}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("capture: %v", err)
	}
	must(t, QueueIdle(ctx, db, orm.ConversationIdleEvent{UserID: "u"}))
	must(t, Cleanup(ctx, db))
	if _, err := requireDirectory(db, "u", d.ID); err != nil {
		t.Fatalf("disabled cleanup removed data: %v", err)
	}
	var job orm.AsyncJob
	must(t, db.Where("id=?", j.ID).Take(&job).Error)
	must(t, db.Where("id=?", b.ID).Take(&b).Error)
	if job.Status != "canceled" || job.LockUntil != nil || b.Status != "canceled" {
		t.Fatalf("work survived pause: job=%+v batch=%+v", job, b)
	}
	var pending int64
	must(t, db.Model(&orm.DocumentObservation{}).Where("user_id=? AND batch_id=?", "u", "").Count(&pending).Error)
	if pending != 0 {
		t.Fatalf("%d observations would be replayed", pending)
	}
	if _, err := Execute(ctx, db, "other", Command{Operation: "create", Name: "other", Intent: "other"}); err != nil {
		t.Fatal(err)
	}
	must(t, db.Model(&orm.UserUIPreferences{}).Where("user_id=?", "u").Update("document_directories_enabled", true).Error)
	if err := mutate(ctx, db, "u", func(tx *gorm.DB) error { return ownsJob(tx, j) }); !errors.Is(err, maintenanceLeaseLost) {
		t.Fatalf("old result can commit after reenable: %v", err)
	}
	oldRun := context.WithValue(ctx, controlGenerationKey{}, int64(0))
	if err := SaveObservations(oldRun, db, "u", "c", "h", "r", "", []Observation{{CallID: "late"}}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("old chat flushed after reenable: %v", err)
	}
	if _, err := folderOperation(oldRun, db, "u", "c", Command{Operation: "folder_checkpoint"}, FolderRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("old folder callback accepted: %v", err)
	}
	newRun := context.WithValue(ctx, controlGenerationKey{}, int64(1))
	must(t, SaveObservations(newRun, db, "u", "c", "h2", "r2", "", []Observation{{CallID: "new"}}))
	if _, err := Get(ctx, db, "u", d.ID); err != nil {
		t.Fatalf("retained directory unavailable: %v", err)
	}
}

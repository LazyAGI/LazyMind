package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/compat/contract"
	compatskill "lazymind/core/compat/skill"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

type fakeSkillService struct {
	listReqs []skillservice.ListSkillsRequest
	getReqs  []skillservice.GetSkillRequest
	items    []skillservice.SkillSummary
	total    int64
	detail   skillservice.SkillDetail
	listErr  error
	getErr   error
}

func (s *fakeSkillService) ListSkills(ctx context.Context, req skillservice.ListSkillsRequest) (skillservice.ListSkillsResponse, error) {
	s.listReqs = append(s.listReqs, req)
	if s.listErr != nil {
		return skillservice.ListSkillsResponse{}, s.listErr
	}
	total := s.total
	if total == 0 {
		total = int64(len(s.items))
	}
	start := req.Offset
	if start > len(s.items) {
		start = len(s.items)
	}
	end := len(s.items)
	if req.Limit > 0 && start+req.Limit < end {
		end = start + req.Limit
	}
	return skillservice.ListSkillsResponse{Items: s.items[start:end], Total: total}, nil
}

func (s *fakeSkillService) GetSkill(ctx context.Context, req skillservice.GetSkillRequest) (skillservice.SkillDetail, error) {
	s.getReqs = append(s.getReqs, req)
	if s.getErr != nil {
		return skillservice.SkillDetail{}, s.getErr
	}
	if s.detail.ID == "" {
		return skillservice.SkillDetail{SkillSummary: skillservice.SkillSummary{ID: req.SkillID, Name: "demo"}}, nil
	}
	return s.detail, nil
}

type fakeRevisionReader struct {
	readReqs []skillrevision.ReadRevisionFileRequest
	file     skillrevision.FileContent
	readErr  error
}

func (r *fakeRevisionReader) ReadRevisionFile(ctx context.Context, req skillrevision.ReadRevisionFileRequest) (skillrevision.FileContent, error) {
	r.readReqs = append(r.readReqs, req)
	if r.readErr != nil {
		return skillrevision.FileContent{}, r.readErr
	}
	return r.file, nil
}

type forbiddenRevisionReader struct{}

func (forbiddenRevisionReader) ReadRevisionFile(ctx context.Context, req skillrevision.ReadRevisionFileRequest) (skillrevision.FileContent, error) {
	return skillrevision.FileContent{}, errors.New("revision reader should not be called")
}

func TestSkillAdapterListPassesUserID(t *testing.T) {
	service := &fakeSkillService{}
	adapter := mustAdapter(t, service)
	_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(service.listReqs) != 1 || service.listReqs[0].UserID != "user-1" {
		t.Fatalf("ListSkills reqs = %#v, want user-1", service.listReqs)
	}
}

func TestSkillAdapterListPassesFiltersAndPagingToService(t *testing.T) {
	service := &fakeSkillService{items: []skillservice.SkillSummary{{ID: "name", Name: "Alpha Writer"}}}
	adapter := mustAdapter(t, service)
	_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{
		Keyword:  "  ALPHA  ",
		Category: "writing",
		Tags:     []string{"team", "draft"},
		Page:     contract.PageRequest{PageSize: 20, PageToken: contract.EncodeOffsetPageToken(40)},
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(service.listReqs) != 1 {
		t.Fatalf("ListSkills calls = %d, want 1", len(service.listReqs))
	}
	req := service.listReqs[0]
	if req.Keyword != "ALPHA" || req.Category != "writing" || req.Offset != 40 || req.Limit != 20 {
		t.Fatalf("ListSkills req = %#v, want compat filters and page", req)
	}
	if len(req.Tags) != 2 || req.Tags[0] != "team" || req.Tags[1] != "draft" {
		t.Fatalf("Tags = %#v, want copied tags", req.Tags)
	}
}

func TestSkillAdapterListDeadlineExceededMapsToBackendUnavailable(t *testing.T) {
	service := &fakeSkillService{listErr: fmt.Errorf("wrapped: %w", context.DeadlineExceeded)}
	adapter := mustAdapter(t, service)
	_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{
		Keyword: "alpha",
		Page:    contract.PageRequest{PageSize: 20},
	})
	if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable {
		t.Fatalf("error code = %v, %v; want BACKEND_UNAVAILABLE", code, ok)
	}
	if compatErr, ok := err.(*contract.Error); !ok || compatErr.Message != "backend unavailable" {
		t.Fatalf("err = %#v, want sanitized backend unavailable message", err)
	}
}

func TestSkillAdapterMapsOnlyDeterministicErrors(t *testing.T) {
	compatErr := contract.NewError(contract.Unsupported, "test", "unsupported", false, errors.New("cause"))
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  contract.ErrorCode
		wantSame  error
		retryable bool
	}{
		{name: "wrapped gorm not found", err: fmt.Errorf("wrapped: %w", gorm.ErrRecordNotFound), wantCode: contract.NotFound},
		{name: "plain not found text", err: errors.New("not found"), wantCode: contract.Internal},
		{name: "wrapped deadline", err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), wantCode: contract.BackendUnavailable, retryable: true},
		{name: "plain timeout text", err: errors.New("timeout"), wantCode: contract.Internal},
		{name: "compat passthrough", err: compatErr, wantCode: contract.Unsupported, wantSame: compatErr},
		{name: "unknown", err: errors.New("connection refused"), wantCode: contract.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := mustAdapter(t, &fakeSkillService{listErr: tc.err})
			_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{})
			if tc.wantSame != nil && !errors.Is(err, tc.wantSame) {
				t.Fatalf("err = %v, want passthrough %v", err, tc.wantSame)
			}
			code, ok := contract.CodeOf(err)
			if !ok || code != tc.wantCode {
				t.Fatalf("error code = %v, %v; want %s", code, ok, tc.wantCode)
			}
			if compat, ok := err.(*contract.Error); ok && compat.Retryable != tc.retryable {
				t.Fatalf("retryable = %v, want %v", compat.Retryable, tc.retryable)
			}
		})
	}
}

func TestSkillAdapterPaginationDefaultMaxAndTotal(t *testing.T) {
	items := make([]skillservice.SkillSummary, 150)
	for i := range items {
		items[i] = skillservice.SkillSummary{ID: string(rune('a' + i%26)), Name: "demo"}
	}
	service := &fakeSkillService{items: items}
	adapter := mustAdapter(t, service)

	first, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{})
	if err != nil {
		t.Fatalf("List default returned error: %v", err)
	}
	if len(first.Items) != contract.DefaultPageSize {
		t.Fatalf("default page len = %d, want %d", len(first.Items), contract.DefaultPageSize)
	}
	if first.Page.Total == nil || *first.Page.Total != int64(len(items)) {
		t.Fatalf("total = %v, want %d", first.Page.Total, len(items))
	}
	if service.listReqs[0].Limit != contract.DefaultPageSize {
		t.Fatalf("default Limit = %d, want %d", service.listReqs[0].Limit, contract.DefaultPageSize)
	}

	maxed, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{
		Page: contract.PageRequest{PageSize: 101},
	})
	if err != nil {
		t.Fatalf("List max returned error: %v", err)
	}
	if len(maxed.Items) != contract.MaxPageSize {
		t.Fatalf("max page len = %d, want %d", len(maxed.Items), contract.MaxPageSize)
	}
	if service.listReqs[1].Limit != contract.MaxPageSize {
		t.Fatalf("max Limit = %d, want %d", service.listReqs[1].Limit, contract.MaxPageSize)
	}
}

func TestSkillAdapterPaginationUsesNextPageToken(t *testing.T) {
	items := []skillservice.SkillSummary{
		{ID: "a", Name: "demo"},
		{ID: "b", Name: "demo"},
		{ID: "c", Name: "demo"},
	}
	service := &fakeSkillService{items: items}
	adapter := mustAdapter(t, service)
	first, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{
		Page: contract.PageRequest{PageSize: 2},
	})
	if err != nil {
		t.Fatalf("first List returned error: %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != "a" || first.Items[1].ID != "b" {
		t.Fatalf("first items = %#v, want a,b", first.Items)
	}
	if first.Page.NextPageToken == "" {
		t.Fatalf("NextPageToken is empty")
	}
	second, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{
		Page: contract.PageRequest{PageSize: 2, PageToken: first.Page.NextPageToken},
	})
	if err != nil {
		t.Fatalf("second List returned error: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "c" {
		t.Fatalf("second items = %#v, want c", second.Items)
	}
	if second.Page.NextPageToken != "" {
		t.Fatalf("second NextPageToken = %q, want empty", second.Page.NextPageToken)
	}
	if service.listReqs[1].Offset != 2 {
		t.Fatalf("second Offset = %d, want 2", service.listReqs[1].Offset)
	}
}

func TestSkillAdapterInvalidPageToken(t *testing.T) {
	adapter := mustAdapter(t, &fakeSkillService{})
	_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{
		Page: contract.PageRequest{PageSize: 20, PageToken: "not-valid"},
	})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("error code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestSkillAdapterGetMetadataPassesUserID(t *testing.T) {
	service := &fakeSkillService{}
	adapter := mustAdapter(t, service)
	summary, err := adapter.GetMetadata(context.Background(), contract.CallContext{UserID: "user-1"}, "skill-1")
	if err != nil {
		t.Fatalf("GetMetadata returned error: %v", err)
	}
	if summary.ID != "skill-1" || summary.Name != "demo" {
		t.Fatalf("summary = %#v, want metadata only", summary)
	}
	if len(service.getReqs) != 1 || service.getReqs[0].UserID != "user-1" || service.getReqs[0].SkillID != "skill-1" {
		t.Fatalf("GetSkill reqs = %#v, want user/skill", service.getReqs)
	}
}

func TestSkillAdapterGetMetadataNotFoundMapsToCompatNotFound(t *testing.T) {
	adapter := mustAdapter(t, &fakeSkillService{getErr: gorm.ErrRecordNotFound})
	_, err := adapter.GetMetadata(context.Background(), contract.CallContext{UserID: "user-1"}, "missing")
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("error code = %v, %v; want NOT_FOUND", code, ok)
	}
	if compatErr, ok := err.(*contract.Error); !ok || compatErr.Message != "skill not found" {
		t.Fatalf("err = %#v, want sanitized not found message", err)
	}
}

func TestSkillAdapterReadContentReadsSkillMD(t *testing.T) {
	service := &fakeSkillService{}
	reader := &fakeRevisionReader{file: skillrevision.FileContent{Path: "SKILL.md", Content: "hello"}}
	adapter := mustAdapterWithReader(t, service, reader)
	content, err := adapter.ReadContent(context.Background(), contract.CallContext{UserID: "user-1"}, "skill-1", "revA")
	if err != nil {
		t.Fatalf("ReadContent returned error: %v", err)
	}
	if content.Path != "SKILL.md" || content.Text != "hello" || content.RevisionID != "revA" {
		t.Fatalf("content = %#v, want revA SKILL.md text", content)
	}
	if len(service.getReqs) != 1 || service.getReqs[0].UserID != "user-1" {
		t.Fatalf("GetSkill reqs = %#v, want ownership check", service.getReqs)
	}
	if len(reader.readReqs) != 1 || reader.readReqs[0].RevisionID != "revA" || reader.readReqs[0].Path != "SKILL.md" {
		t.Fatalf("ReadRevisionFile reqs = %#v, want revA SKILL.md", reader.readReqs)
	}
}

func TestSkillAdapterReadContentChecksOwnerBeforeRevisionRead(t *testing.T) {
	service := &fakeSkillService{getErr: gorm.ErrRecordNotFound}
	adapter := mustAdapterWithReader(t, service, forbiddenRevisionReader{})
	_, err := adapter.ReadContent(context.Background(), contract.CallContext{UserID: "user-1"}, "skill-1", "revA")
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("error code = %v, %v; want NOT_FOUND", code, ok)
	}
	if len(service.getReqs) != 1 {
		t.Fatalf("GetSkill calls = %d, want 1", len(service.getReqs))
	}
}

func TestSkillAdapterReadContentRevisionNotFoundMapsToCompatNotFound(t *testing.T) {
	reader := &fakeRevisionReader{readErr: gorm.ErrRecordNotFound}
	adapter := mustAdapterWithReader(t, &fakeSkillService{}, reader)
	_, err := adapter.ReadContent(context.Background(), contract.CallContext{UserID: "user-1"}, "skill-1", "deleted-rev")
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("error code = %v, %v; want NOT_FOUND", code, ok)
	}
}

func TestSkillAdapterReturnsCompatErrorUnchanged(t *testing.T) {
	want := contract.NewError(contract.Unsupported, "test", "unsupported", false, errors.New("cause"))
	adapter := mustAdapter(t, &fakeSkillService{listErr: want})
	_, err := adapter.List(context.Background(), contract.CallContext{UserID: "user-1"}, compatskill.ListInput{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want original compat error", err)
	}
}

func TestNewSkillAdapterRejectsNilDependencies(t *testing.T) {
	if _, err := NewSkillAdapter(nil, &fakeRevisionReader{}); err == nil {
		t.Fatalf("NewSkillAdapter nil service error = nil, want error")
	}
	if _, err := NewSkillAdapter(&fakeSkillService{}, nil); err == nil {
		t.Fatalf("NewSkillAdapter nil revision reader error = nil, want error")
	}
}

func TestNewSkillAdapterForDBRejectsNilDependencies(t *testing.T) {
	if _, err := NewSkillAdapterForDB(nil, &gorm.DB{}); err == nil {
		t.Fatalf("NewSkillAdapterForDB nil service error = nil, want error")
	}
	if _, err := NewSkillAdapterForDB(&skillservice.SkillService{}, nil); err == nil {
		t.Fatalf("NewSkillAdapterForDB nil db error = nil, want error")
	}
}

func TestNewSkillAdapterForDBInjectsRevisionReader(t *testing.T) {
	db := testutil.NewTestDB(t)
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        db.DB,
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
	})
	adapter, err := NewSkillAdapterForDB(service, db.DB)
	if err != nil {
		t.Fatalf("NewSkillAdapterForDB returned error: %v", err)
	}
	if adapter.revisionReader == nil {
		t.Fatalf("revisionReader is nil")
	}
}

func TestNewSkillAdapterForDBDoesNotMutateSQLiteSkillUniqueIndexes(t *testing.T) {
	db := testutil.NewTestDB(t)
	assertSQLiteIndexExists(t, db.DB, "uk_skills_owner_identity")
	assertSQLiteIndexExists(t, db.DB, "uk_skills_owner_relative_root")
	assertSQLiteTableSQLContains(t, db.DB, "skill_blobs", "skill_blob_storage_shape")

	_ = skillrevision.NewService(skillrevision.ServiceDeps{
		DB:        db.DB,
		BlobStore: skillrevision.NewBlobStore(db.DB, skillrevision.NewLocalObjectStore(t.TempDir())),
	})
	assertSQLiteIndexExists(t, db.DB, "uk_skills_owner_identity")
	assertSQLiteIndexExists(t, db.DB, "uk_skills_owner_relative_root")
	assertSQLiteTableSQLContains(t, db.DB, "skill_blobs", "skill_blob_storage_shape")

	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        db.DB,
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
	})
	if _, err := NewSkillAdapterForDB(service, db.DB); err != nil {
		t.Fatalf("NewSkillAdapterForDB returned error: %v", err)
	}
	assertSQLiteIndexExists(t, db.DB, "uk_skills_owner_identity")
	assertSQLiteIndexExists(t, db.DB, "uk_skills_owner_relative_root")
	assertSQLiteTableSQLContains(t, db.DB, "skill_blobs", "skill_blob_storage_shape")

	insertSkillFixture(t, db.DB, "skill-1", "owner-1", "writing", "planner", "writing/planner")
	if err := insertSkillRow(db.DB, "skill-2", "owner-1", "writing", "planner", "writing/planner-2"); err == nil {
		t.Fatal("duplicate owner/category/skill_name insert succeeded, want unique constraint failure")
	}
	if err := insertSkillRow(db.DB, "skill-3", "owner-1", "writing", "planner-3", "writing/planner"); err == nil {
		t.Fatal("duplicate owner/relative_root insert succeeded, want unique constraint failure")
	}
}

func assertSQLiteIndexExists(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	var count int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "index", name).Scan(&count).Error; err != nil {
		t.Fatalf("query sqlite index %s: %v", name, err)
	}
	if count != 1 {
		t.Fatalf("sqlite index %s count = %d, want 1", name, count)
	}
}

func assertSQLiteTableSQLContains(t *testing.T, db *gorm.DB, table, needle string) {
	t.Helper()
	var sql string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = ? AND name = ?", "table", table).Scan(&sql).Error; err != nil {
		t.Fatalf("query sqlite table %s: %v", table, err)
	}
	if !strings.Contains(sql, needle) {
		t.Fatalf("sqlite table %s SQL does not contain %q: %s", table, needle, sql)
	}
}

func insertSkillFixture(t *testing.T, db *gorm.DB, id, owner, category, name, relativeRoot string) {
	t.Helper()
	if err := insertSkillRow(db, id, owner, category, name, relativeRoot); err != nil {
		t.Fatalf("insert skill fixture %s: %v", id, err)
	}
}

func insertSkillRow(db *gorm.DB, id, owner, category, name, relativeRoot string) error {
	now := testutil.TimeFixture()
	return db.Create(&testutil.SkillRow{
		ID:                 id,
		OwnerUserID:        owner,
		CreateUserID:       owner,
		Category:           category,
		SkillName:          name,
		Tags:               []byte("[]"),
		RelativeRoot:       relativeRoot,
		SkillMDPath:        "SKILL.md",
		AutoEvoApplyStatus: "idle",
		IsEnabled:          true,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error
}

func mustAdapter(t *testing.T, service SkillService) *SkillAdapter {
	t.Helper()
	return mustAdapterWithReader(t, service, &fakeRevisionReader{})
}

func mustAdapterWithReader(t *testing.T, service SkillService, reader RevisionReader) *SkillAdapter {
	t.Helper()
	adapter, err := NewSkillAdapter(service, reader)
	if err != nil {
		t.Fatalf("NewSkillAdapter: %v", err)
	}
	return adapter
}

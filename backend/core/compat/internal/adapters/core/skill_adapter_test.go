package core

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/compat/contract"
	compatskill "lazymind/core/compat/skill"
	skillservice "lazymind/core/skillv2/service"
)

type fakeSkillService struct {
	listReqs []skillservice.ListSkillsRequest
	getReqs  []skillservice.GetSkillRequest
	readRefs []skillservice.FileRef
	items    []skillservice.SkillSummary
	total    int64
	detail   skillservice.SkillDetail
	file     skillservice.FileContent
	listErr  error
	getErr   error
	readErr  error
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

func (s *fakeSkillService) ReadFile(ctx context.Context, ref skillservice.FileRef) (skillservice.FileContent, error) {
	s.readRefs = append(s.readRefs, ref)
	if s.readErr != nil {
		return skillservice.FileContent{}, s.readErr
	}
	return s.file, nil
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

func TestSkillAdapterListErrorMapsToBackendUnavailable(t *testing.T) {
	service := &fakeSkillService{listErr: errors.New("connection refused")}
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

func TestSkillAdapterGetPassesUserID(t *testing.T) {
	service := &fakeSkillService{}
	adapter := mustAdapter(t, service)
	_, err := adapter.Get(context.Background(), contract.CallContext{UserID: "user-1"}, "skill-1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if len(service.getReqs) != 1 || service.getReqs[0].UserID != "user-1" || service.getReqs[0].SkillID != "skill-1" {
		t.Fatalf("GetSkill reqs = %#v, want user/skill", service.getReqs)
	}
}

func TestSkillAdapterGetNotFoundMapsToCompatNotFound(t *testing.T) {
	adapter := mustAdapter(t, &fakeSkillService{getErr: gorm.ErrRecordNotFound})
	_, err := adapter.Get(context.Background(), contract.CallContext{UserID: "user-1"}, "missing")
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("error code = %v, %v; want NOT_FOUND", code, ok)
	}
	if compatErr, ok := err.(*contract.Error); !ok || compatErr.Message != "skill not found" {
		t.Fatalf("err = %#v, want sanitized not found message", err)
	}
}

func TestSkillAdapterReadContentReadsSkillMD(t *testing.T) {
	service := &fakeSkillService{file: skillservice.FileContent{Path: "SKILL.md", Content: "hello"}}
	adapter := mustAdapter(t, service)
	content, err := adapter.ReadContent(context.Background(), contract.CallContext{UserID: "user-1"}, "skill-1")
	if err != nil {
		t.Fatalf("ReadContent returned error: %v", err)
	}
	if content.Path != "SKILL.md" || content.Text != "hello" {
		t.Fatalf("content = %#v, want SKILL.md text", content)
	}
	if len(service.getReqs) != 1 || service.getReqs[0].UserID != "user-1" {
		t.Fatalf("GetSkill reqs = %#v, want ownership check", service.getReqs)
	}
	if len(service.readRefs) != 1 || service.readRefs[0].RefType != "head" || service.readRefs[0].Path != "SKILL.md" {
		t.Fatalf("ReadFile refs = %#v, want head SKILL.md", service.readRefs)
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
	if _, err := NewSkillAdapter(nil); err == nil {
		t.Fatalf("NewSkillAdapter nil service error = nil, want error")
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

func mustAdapter(t *testing.T, service SkillService) *SkillAdapter {
	t.Helper()
	adapter, err := NewSkillAdapter(service)
	if err != nil {
		t.Fatalf("NewSkillAdapter: %v", err)
	}
	return adapter
}

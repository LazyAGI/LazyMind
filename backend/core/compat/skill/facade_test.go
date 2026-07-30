package skill

import (
	"context"
	"errors"
	"testing"

	"lazymind/core/compat/contract"
)

type fakePort struct {
	listInput           ListInput
	getSkillID          string
	readSkillID         string
	readRevisionID      string
	readContentCalls    int
	currentHeadRevision string
	emptyHeadRevision   bool
	contentRevisionID   string
	listErr             error
	getErr              error
	readErr             error
}

func (p *fakePort) List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error) {
	p.listInput = input
	if p.listErr != nil {
		return ListResult{}, p.listErr
	}
	return ListResult{Page: contract.PageResult{}}, nil
}

func (p *fakePort) GetMetadata(ctx context.Context, callCtx contract.CallContext, skillID string) (Summary, error) {
	p.getSkillID = skillID
	if p.getErr != nil {
		return Summary{}, p.getErr
	}
	revisionID := p.currentHeadRevision
	if revisionID == "" && !p.emptyHeadRevision {
		revisionID = "revA"
	}
	p.currentHeadRevision = "revB"
	return Summary{ID: skillID, Name: "demo", HeadRevisionID: revisionID}, nil
}

func (p *fakePort) ReadContent(ctx context.Context, callCtx contract.CallContext, skillID, revisionID string) (Content, error) {
	p.readContentCalls++
	p.readSkillID = skillID
	p.readRevisionID = revisionID
	if p.readErr != nil {
		return Content{}, p.readErr
	}
	contentRevisionID := p.contentRevisionID
	if contentRevisionID == "" {
		contentRevisionID = revisionID
	}
	return Content{Path: "SKILL.md", RevisionID: contentRevisionID, Text: "content"}, nil
}

func TestFacadeListRequiresUserID(t *testing.T) {
	facade := mustFacade(t, &fakePort{})
	_, err := facade.List(context.Background(), contract.CallContext{UserID: "  "}, ListInput{})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("error code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestFacadeGetRequiresSkillID(t *testing.T) {
	facade := mustFacade(t, &fakePort{})
	_, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: "  "})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("error code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestNewFacadeRejectsNilPort(t *testing.T) {
	_, err := NewFacade(nil)
	if code, ok := contract.CodeOf(err); !ok || code != contract.Internal {
		t.Fatalf("error code = %v, %v; want INTERNAL", code, ok)
	}
}

func TestFacadeListNormalizesPaging(t *testing.T) {
	port := &fakePort{}
	facade := mustFacade(t, port)
	_, err := facade.List(context.Background(), contract.CallContext{UserID: " user "}, ListInput{
		Keyword:  " query ",
		Category: " docs ",
		Page:     contract.PageRequest{PageSize: 500, PageToken: "  opaque  "},
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if port.listInput.Page.PageSize != contract.MaxPageSize {
		t.Fatalf("PageSize = %d, want %d", port.listInput.Page.PageSize, contract.MaxPageSize)
	}
	if port.listInput.Page.PageToken != "  opaque  " {
		t.Fatalf("PageToken = %q, want original", port.listInput.Page.PageToken)
	}
	if port.listInput.Keyword != "query" || port.listInput.Category != "docs" {
		t.Fatalf("filters = %q/%q, want trimmed", port.listInput.Keyword, port.listInput.Category)
	}
}

func TestFacadeGetWithoutContentDoesNotReadContent(t *testing.T) {
	port := &fakePort{}
	facade := mustFacade(t, port)
	result, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: " skill-1 "})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if result.Content != nil {
		t.Fatalf("Content = %#v, want nil", result.Content)
	}
	if port.getSkillID != "skill-1" {
		t.Fatalf("GetMetadata skillID = %q, want trimmed", port.getSkillID)
	}
	if port.readContentCalls != 0 {
		t.Fatalf("ReadContent calls = %d, want 0", port.readContentCalls)
	}
}

func TestFacadeGetWithoutContentAllowsEmptyHeadRevision(t *testing.T) {
	port := &fakePort{emptyHeadRevision: true}
	facade := mustFacade(t, port)
	result, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: "skill-1"})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if result.Skill.HeadRevisionID != "" || result.Content != nil {
		t.Fatalf("result = %#v, want metadata without content and empty head", result)
	}
	if port.readContentCalls != 0 {
		t.Fatalf("ReadContent calls = %d, want 0", port.readContentCalls)
	}
}

func TestFacadeGetWithContentAndEmptyHeadReturnsNotFoundWithoutReadContent(t *testing.T) {
	port := &fakePort{emptyHeadRevision: true}
	facade := mustFacade(t, port)
	_, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: "skill-1", IncludeContent: true})
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("error code = %v, %v; want NOT_FOUND", code, ok)
	}
	if compatErr, ok := err.(*contract.Error); !ok || compatErr.Operation != "skill.get" || compatErr.Message != "skill content is not available" {
		t.Fatalf("err = %#v, want stable skill.get content unavailable error", err)
	}
	if port.readContentCalls != 0 {
		t.Fatalf("ReadContent calls = %d, want 0", port.readContentCalls)
	}
}

func TestFacadeGetWithContentUsesMetadataRevision(t *testing.T) {
	port := &fakePort{currentHeadRevision: "revA"}
	facade := mustFacade(t, port)
	result, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: "skill-1", IncludeContent: true})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if port.readContentCalls != 1 || port.readSkillID != "skill-1" || port.readRevisionID != "revA" {
		t.Fatalf("ReadContent calls/id/revision = %d/%q/%q, want 1/skill-1/revA", port.readContentCalls, port.readSkillID, port.readRevisionID)
	}
	if result.Skill.HeadRevisionID != "revA" {
		t.Fatalf("HeadRevisionID = %q, want revA", result.Skill.HeadRevisionID)
	}
	if result.Content == nil || result.Content.Text != "content" || result.Content.RevisionID != "revA" {
		t.Fatalf("Content = %#v, want SKILL.md content", result.Content)
	}
}

func TestFacadeGetRejectsContentRevisionMismatch(t *testing.T) {
	port := &fakePort{currentHeadRevision: "revA", contentRevisionID: "revB"}
	facade := mustFacade(t, port)
	_, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: "skill-1", IncludeContent: true})
	if code, ok := contract.CodeOf(err); !ok || code != contract.Internal {
		t.Fatalf("error code = %v, %v; want INTERNAL", code, ok)
	}
	if port.readContentCalls != 1 || port.readRevisionID != "revA" {
		t.Fatalf("ReadContent calls/revision = %d/%q, want 1/revA", port.readContentCalls, port.readRevisionID)
	}
}

func TestFacadeReturnsPortError(t *testing.T) {
	want := errors.New("backend failed")
	facade := mustFacade(t, &fakePort{getErr: want})
	_, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{SkillID: "skill-1"})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func mustFacade(t *testing.T, port Port) *Facade {
	t.Helper()
	facade, err := NewFacade(port)
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	return facade
}

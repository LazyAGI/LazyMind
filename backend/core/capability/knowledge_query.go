package capability

import (
	"context"
	"lazymind/core/learning"
	"slices"
	"strings"
)

// ResourceRef always uses Core IDs, never algorithm KB IDs.
type ResourceRef struct {
	Kind            string `json:"kind" jsonschema:"knowledge_base, folder or document"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	FolderID        string `json:"folder_id,omitempty"`
	DocumentID      string `json:"document_id,omitempty"`
}

func normalizeKnowledgeIDs(ids []string, op string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	if len(ids) == 0 || len(ids) > maxKnowledgeIDs {
		return nil, NewError(InvalidArgument, op, "provide one to twenty knowledge IDs", false, nil)
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		v, err := boundedRequired(id, maxIDBytes, op, "knowledge ID")
		if err != nil {
			return nil, err
		}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}
func normalizeMatch(value, op string) (string, error) {
	if value == "" {
		return "contains", nil
	}
	if value != "contains" && value != "exact" {
		return "", NewError(InvalidArgument, op, "match must be contains or exact", false, nil)
	}
	return value, nil
}
func (s *Service) ListKnowledge(ctx context.Context, call InvocationContext, input ListKnowledgeInput) (ListKnowledgeResult, error) {
	const op = "knowledge.list"
	if err := validateCaller(call, op); err != nil {
		return ListKnowledgeResult{}, err
	}
	var err error
	input.Keyword, err = boundedOptional(input.Keyword, maxFilterBytes, op, "keyword")
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	input.Tags, err = normalizeTags(input.Tags, op)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	input.KnowledgeBaseIDs, err = normalizeKnowledgeIDs(input.KnowledgeBaseIDs, op)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	if call.AllowedDatasetIDs != nil {
		if input.KnowledgeBaseIDs == nil {
			input.KnowledgeBaseIDs = append([]string{}, call.AllowedDatasetIDs...)
		} else {
			for _, id := range input.KnowledgeBaseIDs {
				if !slices.Contains(call.AllowedDatasetIDs, id) {
					return ListKnowledgeResult{}, NewError(PermissionDenied, op, "library outside execution scope", false, nil)
				}
			}
		}
		if len(input.KnowledgeBaseIDs) == 0 {
			return ListKnowledgeResult{Items: []KnowledgeSummary{}}, nil
		}
	}
	input.Match, err = normalizeMatch(input.Match, op)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	if input.Scene != "" {
		input.Scene, err = learning.NormalizeScene(input.Scene)
		if err != nil {
			return ListKnowledgeResult{}, NewError(InvalidArgument, op, err.Error(), false, nil)
		}
	}
	if input.RequiredAction == "" {
		input.RequiredAction = "read"
	}
	if input.RequiredAction != "read" && input.RequiredAction != "upload" && input.RequiredAction != "write" {
		return ListKnowledgeResult{}, NewError(InvalidArgument, op, "invalid required_action", false, nil)
	}
	input.Page.PageSize, err = normalizePageSize(input.Page.PageSize, op)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	token := input.Page.PageToken
	input.Page.PageToken = ""
	fingerprint, err := pageFingerprint(struct {
		UserID, TenantID string
		Input            ListKnowledgeInput
	}{call.Principal.UserID, call.Principal.TenantID, input})
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	offset, err := pageOffset(token, "knowledge", fingerprint, op)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	page, err := s.knowledge.ListKnowledge(ctx, call, KnowledgeListQuery{Source: input.Source, OrderBy: input.OrderBy, IncludeConfiguration: input.IncludeConfiguration, Keyword: input.Keyword, Tags: input.Tags, KnowledgeBaseIDs: input.KnowledgeBaseIDs, Match: input.Match, Scene: input.Scene, RequiredAction: input.RequiredAction, Offset: offset, Limit: input.Page.PageSize})
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	result := ListKnowledgeResult{Items: nonNilKnowledge(page.Items), Page: PageInfo{Total: page.Total}}
	if page.HasMore {
		result.Page.NextPageToken, err = encodePageToken("knowledge", fingerprint, page.NextOffset)
	}
	return result, err
}
func (s *Service) ListKnowledgeDocuments(ctx context.Context, call InvocationContext, input ListKnowledgeDocumentsInput) (ListKnowledgeDocumentsResult, error) {
	const op = "knowledge.document.list"
	if err := validateCaller(call, op); err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	if (strings.TrimSpace(input.KnowledgeID) != "") == (input.KnowledgeIDs != nil) {
		return ListKnowledgeDocumentsResult{}, NewError(InvalidArgument, op, "provide exactly one of knowledge_id and knowledge_ids", false, nil)
	}
	ids := input.KnowledgeIDs
	if ids == nil {
		ids = []string{input.KnowledgeID}
	}
	ids, err := normalizeKnowledgeIDs(ids, op)
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	if call.AllowedDatasetIDs != nil {
		for _, id := range ids {
			if !slices.Contains(call.AllowedDatasetIDs, id) {
				return ListKnowledgeDocumentsResult{}, NewError(PermissionDenied, op, "library outside execution scope", false, nil)
			}
		}
	}
	if input.KnowledgeIDs == nil {
		input.KnowledgeID = ids[0]
	} else {
		input.KnowledgeIDs = ids
	}
	if input.ParentID != nil && len(ids) != 1 {
		return ListKnowledgeDocumentsResult{}, NewError(InvalidArgument, op, "parent_id requires one library", false, nil)
	}
	input.Name, err = boundedOptional(input.Name, maxFilterBytes, op, "name")
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	input.Path, err = boundedOptional(input.Path, 1024, op, "path")
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	input.Match, err = normalizeMatch(input.Match, op)
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	if input.ResourceType == "" {
		input.ResourceType = "document"
	}
	if input.ResourceType != "document" && input.ResourceType != "folder" && input.ResourceType != "all" {
		return ListKnowledgeDocumentsResult{}, NewError(InvalidArgument, op, "invalid resource_type", false, nil)
	}
	if len(input.ResourceIDs) > 100 {
		return ListKnowledgeDocumentsResult{}, NewError(InvalidArgument, op, "at most 100 resource IDs", false, nil)
	}
	for i, id := range input.ResourceIDs {
		input.ResourceIDs[i], err = boundedRequired(id, maxIDBytes, op, "resource ID")
		if err != nil {
			return ListKnowledgeDocumentsResult{}, err
		}
	}
	if input.ParentID != nil {
		v, e := boundedOptional(*input.ParentID, maxIDBytes, op, "parent_id")
		if e != nil {
			return ListKnowledgeDocumentsResult{}, e
		}
		input.ParentID = &v
	}
	input.Page.PageSize, err = normalizePageSize(input.Page.PageSize, op)
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	token := input.Page.PageToken
	input.Page.PageToken = ""
	fingerprint, err := pageFingerprint(struct {
		UserID, TenantID string
		Input            ListKnowledgeDocumentsInput
	}{call.Principal.UserID, call.Principal.TenantID, input})
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	offset, err := pageOffset(token, "knowledge-documents", fingerprint, op)
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	recursive := true
	if input.Recursive != nil {
		recursive = *input.Recursive
	}
	page, err := s.documents.ListKnowledgeDocuments(ctx, call, KnowledgeDocumentListQuery{OrderBy: input.OrderBy, KnowledgeID: input.KnowledgeID, KnowledgeIDs: ids, ResourceIDs: input.ResourceIDs, ParentID: input.ParentID, Recursive: recursive, ResourceType: input.ResourceType, Match: input.Match, Name: input.Name, Path: input.Path, Offset: offset, Limit: input.Page.PageSize})
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	result := ListKnowledgeDocumentsResult{Items: nonNilKnowledgeDocuments(page.Items), Page: PageInfo{Total: page.Total}}
	if next := offset + len(page.Items); int64(next) < page.Total {
		result.Page.NextPageToken, err = encodePageToken("knowledge-documents", fingerprint, next)
	}
	return result, err
}

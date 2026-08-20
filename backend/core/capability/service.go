package capability

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	defaultPageSize       = 20
	maxPageSize           = 100
	maxIDBytes            = 256
	maxFilterBytes        = 256
	maxQueryBytes         = 8 << 10
	maxTags               = 20
	maxTagBytes           = 64
	maxPageTokenBytes     = 1 << 10
	maxKnowledgeIDs       = 20
	defaultSearchTopK     = 10
	maxSearchTopK         = 50
	maxSkillContentBytes  = 512 << 10
	maxSearchHitTextBytes = 16 << 10
	maxToolResultBytes    = 1 << 20
)

type Dependencies struct {
	Skills    SkillReader
	Knowledge KnowledgeCatalog
	Documents KnowledgeDocumentReader
	Search    KnowledgeSearcher
}

type Service struct {
	skills    SkillReader
	knowledge KnowledgeCatalog
	documents KnowledgeDocumentReader
	search    KnowledgeSearcher
}

func NewService(deps Dependencies) (*Service, error) {
	switch {
	case deps.Skills == nil:
		return nil, NewError(Internal, "capability.new", "skill reader is required", false, nil)
	case deps.Knowledge == nil:
		return nil, NewError(Internal, "capability.new", "knowledge catalog is required", false, nil)
	case deps.Documents == nil:
		return nil, NewError(Internal, "capability.new", "knowledge document reader is required", false, nil)
	case deps.Search == nil:
		return nil, NewError(Internal, "capability.new", "knowledge searcher is required", false, nil)
	default:
		return &Service{skills: deps.Skills, knowledge: deps.Knowledge, documents: deps.Documents, search: deps.Search}, nil
	}
}

func (s *Service) ListSkills(ctx context.Context, call InvocationContext, input ListSkillsInput) (ListSkillsResult, error) {
	const operation = "skill.list"
	if err := validateCaller(call, operation); err != nil {
		return ListSkillsResult{}, err
	}
	keyword, err := boundedOptional(input.Keyword, maxFilterBytes, operation, "keyword")
	if err != nil {
		return ListSkillsResult{}, err
	}
	category, err := boundedOptional(input.Category, maxFilterBytes, operation, "category")
	if err != nil {
		return ListSkillsResult{}, err
	}
	tags, err := normalizeTags(input.Tags, operation)
	if err != nil {
		return ListSkillsResult{}, err
	}
	pageSize, err := normalizePageSize(input.Page.PageSize, operation)
	if err != nil {
		return ListSkillsResult{}, err
	}
	fingerprint, err := pageFingerprint(struct {
		Keyword  string
		Category string
		Tags     []string
	}{keyword, category, tags})
	if err != nil {
		return ListSkillsResult{}, NewError(Internal, operation, "cannot prepare pagination", false, err)
	}
	offset, err := pageOffset(input.Page.PageToken, "skills", fingerprint, operation)
	if err != nil {
		return ListSkillsResult{}, err
	}
	page, err := s.skills.ListSkills(ctx, call, SkillListQuery{
		Keyword: keyword, Category: category, Tags: tags, Offset: offset, Limit: pageSize,
	})
	if err != nil {
		return ListSkillsResult{}, err
	}
	result := ListSkillsResult{Items: nonNilSkills(page.Items), Page: PageInfo{Total: page.Total}}
	if next := offset + len(page.Items); next < int(page.Total) {
		result.Page.NextPageToken, err = encodePageToken("skills", fingerprint, next)
		if err != nil {
			return ListSkillsResult{}, NewError(Internal, operation, "cannot encode page token", false, err)
		}
	}
	return result, nil
}

func (s *Service) GetSkill(ctx context.Context, call InvocationContext, input GetSkillInput) (GetSkillResult, error) {
	const operation = "skill.get"
	if err := validateCaller(call, operation); err != nil {
		return GetSkillResult{}, err
	}
	skillID, err := boundedRequired(input.SkillID, maxIDBytes, operation, "skill_id")
	if err != nil {
		return GetSkillResult{}, err
	}
	metadata, err := s.skills.GetSkillMetadata(ctx, call, skillID)
	if err != nil {
		return GetSkillResult{}, err
	}
	if !metadata.Published || strings.TrimSpace(metadata.Summary.HeadRevisionID) == "" {
		return GetSkillResult{}, NewError(NotFound, operation, "skill not found", false, nil)
	}
	result := GetSkillResult{Skill: metadata.Summary}
	if !input.IncludeContent {
		return result, nil
	}
	content, err := s.skills.ReadSkillContent(ctx, call, skillID, metadata.Summary.HeadRevisionID)
	if err != nil {
		return GetSkillResult{}, err
	}
	if content.RevisionID != metadata.Summary.HeadRevisionID {
		return GetSkillResult{}, NewError(Internal, operation, "skill content revision mismatch", false, nil)
	}
	if len(content.Text) > maxSkillContentBytes {
		return GetSkillResult{}, NewError(ResultTooLarge, operation, "skill content exceeds 512 KiB", false, nil)
	}
	result.Content = &content
	return result, nil
}

func (s *Service) ListKnowledge(ctx context.Context, call InvocationContext, input ListKnowledgeInput) (ListKnowledgeResult, error) {
	const operation = "knowledge.list"
	if err := validateCaller(call, operation); err != nil {
		return ListKnowledgeResult{}, err
	}
	keyword, err := boundedOptional(input.Keyword, maxFilterBytes, operation, "keyword")
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	tags, err := normalizeTags(input.Tags, operation)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	pageSize, err := normalizePageSize(input.Page.PageSize, operation)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	fingerprint, err := pageFingerprint(struct {
		Keyword string
		Tags    []string
	}{keyword, tags})
	if err != nil {
		return ListKnowledgeResult{}, NewError(Internal, operation, "cannot prepare pagination", false, err)
	}
	offset, err := pageOffset(input.Page.PageToken, "knowledge", fingerprint, operation)
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	page, err := s.knowledge.ListKnowledge(ctx, call, KnowledgeListQuery{Keyword: keyword, Tags: tags, Offset: offset, Limit: pageSize})
	if err != nil {
		return ListKnowledgeResult{}, err
	}
	result := ListKnowledgeResult{Items: nonNilKnowledge(page.Items), Page: PageInfo{Total: page.Total}}
	if page.HasMore {
		result.Page.NextPageToken, err = encodePageToken("knowledge", fingerprint, page.NextOffset)
		if err != nil {
			return ListKnowledgeResult{}, NewError(Internal, operation, "cannot encode page token", false, err)
		}
	}
	return result, nil
}

func (s *Service) ListKnowledgeDocuments(ctx context.Context, call InvocationContext, input ListKnowledgeDocumentsInput) (ListKnowledgeDocumentsResult, error) {
	const operation = "knowledge.document.list"
	if err := validateCaller(call, operation); err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	knowledgeID, err := boundedRequired(input.KnowledgeID, maxIDBytes, operation, "knowledge_id")
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	pageSize, err := normalizePageSize(input.Page.PageSize, operation)
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	fingerprint, err := pageFingerprint(struct{ KnowledgeID string }{knowledgeID})
	if err != nil {
		return ListKnowledgeDocumentsResult{}, NewError(Internal, operation, "cannot prepare pagination", false, err)
	}
	offset, err := pageOffset(input.Page.PageToken, "knowledge-documents", fingerprint, operation)
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	page, err := s.documents.ListKnowledgeDocuments(ctx, call, KnowledgeDocumentListQuery{
		KnowledgeID: knowledgeID, Offset: offset, Limit: pageSize,
	})
	if err != nil {
		return ListKnowledgeDocumentsResult{}, err
	}
	result := ListKnowledgeDocumentsResult{Items: nonNilKnowledgeDocuments(page.Items), Page: PageInfo{Total: page.Total}}
	if next := offset + len(page.Items); next < int(page.Total) {
		result.Page.NextPageToken, err = encodePageToken("knowledge-documents", fingerprint, next)
		if err != nil {
			return ListKnowledgeDocumentsResult{}, NewError(Internal, operation, "cannot encode page token", false, err)
		}
	}
	return result, nil
}

func (s *Service) GetKnowledgeDocument(ctx context.Context, call InvocationContext, input GetKnowledgeDocumentInput) (GetKnowledgeDocumentResult, error) {
	const operation = "knowledge.document.get"
	if err := validateCaller(call, operation); err != nil {
		return GetKnowledgeDocumentResult{}, err
	}
	knowledgeID, err := boundedRequired(input.KnowledgeID, maxIDBytes, operation, "knowledge_id")
	if err != nil {
		return GetKnowledgeDocumentResult{}, err
	}
	documentID, err := boundedRequired(input.DocumentID, maxIDBytes, operation, "document_id")
	if err != nil {
		return GetKnowledgeDocumentResult{}, err
	}
	chunksPage := PageRequest{}
	if input.IncludeChunks {
		chunksPage.PageSize, err = normalizePageSize(input.ChunksPage.PageSize, operation)
		if err != nil {
			return GetKnowledgeDocumentResult{}, err
		}
		if len(input.ChunksPage.PageToken) > maxPageTokenBytes {
			return GetKnowledgeDocumentResult{}, NewError(InvalidArgument, operation, "chunks_page.page_token is too long", false, nil)
		}
		chunksPage.PageToken = strings.TrimSpace(input.ChunksPage.PageToken)
	}
	result, err := s.documents.GetKnowledgeDocument(ctx, call, GetKnowledgeDocumentInput{
		KnowledgeID: knowledgeID, DocumentID: documentID,
		IncludeContent: input.IncludeContent, IncludeChunks: input.IncludeChunks,
		ChunksPage: chunksPage,
	})
	if err != nil {
		return GetKnowledgeDocumentResult{}, err
	}
	if err := ensureResultSize(operation, result); err != nil {
		return GetKnowledgeDocumentResult{}, err
	}
	return result, nil
}

func (s *Service) SearchKnowledge(ctx context.Context, call InvocationContext, input SearchKnowledgeInput) (SearchKnowledgeResult, error) {
	const operation = "knowledge.search"
	if err := validateCaller(call, operation); err != nil {
		return SearchKnowledgeResult{}, err
	}
	query, err := boundedRequired(input.Query, maxQueryBytes, operation, "query")
	if err != nil {
		return SearchKnowledgeResult{}, err
	}
	knowledgeIDs, err := normalizeIDs(input.KnowledgeIDs, operation)
	if err != nil {
		return SearchKnowledgeResult{}, err
	}
	topK := input.TopK
	if topK == 0 {
		topK = defaultSearchTopK
	}
	if topK < 1 || topK > maxSearchTopK {
		return SearchKnowledgeResult{}, NewError(InvalidArgument, operation, "top_k must be between 1 and 50", false, nil)
	}
	result, err := s.search.SearchKnowledge(ctx, call, SearchKnowledgeInput{Query: query, KnowledgeIDs: knowledgeIDs, TopK: topK})
	if err != nil {
		return SearchKnowledgeResult{}, err
	}
	result.Hits = nonNilSearchHits(result.Hits)
	for _, hit := range result.Hits {
		if len(hit.Text) > maxSearchHitTextBytes {
			return SearchKnowledgeResult{}, NewError(ResultTooLarge, operation, "one search hit exceeds 16 KiB", false, nil)
		}
	}
	if err := ensureResultSize(operation, result); err != nil {
		return SearchKnowledgeResult{}, err
	}
	return result, nil
}

func ensureResultSize(operation string, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return NewError(Internal, operation, "cannot encode result", false, err)
	}
	if len(encoded) > maxToolResultBytes {
		return NewError(ResultTooLarge, operation, "result exceeds 1 MiB", false, nil)
	}
	return nil
}

func validateCaller(call InvocationContext, operation string) error {
	if strings.TrimSpace(call.Principal.UserID) == "" {
		return NewError(Unauthenticated, operation, "authenticated user is required", false, nil)
	}
	if !call.Principal.Permissions.Has(RequiredPermission) {
		return NewError(PermissionDenied, operation, "qa.read permission is required", false, nil)
	}
	return nil
}

func boundedRequired(value string, max int, operation, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", NewError(InvalidArgument, operation, field+" is required", false, nil)
	}
	if len(value) > max {
		return "", NewError(InvalidArgument, operation, field+" is too long", false, nil)
	}
	return value, nil
}

func boundedOptional(value string, max int, operation, field string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return "", NewError(InvalidArgument, operation, field+" is too long", false, nil)
	}
	return value, nil
}

func normalizeTags(values []string, operation string) ([]string, error) {
	if len(values) > maxTags {
		return nil, NewError(InvalidArgument, operation, "at most 20 tags are allowed", false, nil)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxTagBytes {
			return nil, NewError(InvalidArgument, operation, "tag is too long", false, nil)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeIDs(values []string, operation string) ([]string, error) {
	if len(values) == 0 {
		return nil, NewError(InvalidArgument, operation, "knowledge_ids is required", false, nil)
	}
	if len(values) > maxKnowledgeIDs {
		return nil, NewError(InvalidArgument, operation, "at most 20 knowledge_ids are allowed", false, nil)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value, err := boundedRequired(value, maxIDBytes, operation, "knowledge_id")
		if err != nil {
			return nil, err
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func normalizePageSize(value int, operation string) (int, error) {
	if value == 0 {
		return defaultPageSize, nil
	}
	if value < 1 || value > maxPageSize {
		return 0, NewError(InvalidArgument, operation, "page_size must be between 1 and 100", false, nil)
	}
	return value, nil
}

func pageOffset(token, kind, fingerprint, operation string) (int, error) {
	if len(token) > maxPageTokenBytes {
		return 0, NewError(InvalidArgument, operation, "page_token is too long", false, nil)
	}
	offset, err := decodePageToken(token, kind, fingerprint)
	if err != nil {
		var capabilityErr *Error
		if errors.As(err, &capabilityErr) {
			return 0, NewError(capabilityErr.Code, operation, "invalid page token", false, err)
		}
		return 0, NewError(InvalidArgument, operation, "invalid page token", false, err)
	}
	return offset, nil
}

func nonNilSkills(items []SkillSummary) []SkillSummary {
	if items == nil {
		return []SkillSummary{}
	}
	return items
}

func nonNilKnowledge(items []KnowledgeSummary) []KnowledgeSummary {
	if items == nil {
		return []KnowledgeSummary{}
	}
	return items
}

func nonNilKnowledgeDocuments(items []KnowledgeDocumentSummary) []KnowledgeDocumentSummary {
	if items == nil {
		return []KnowledgeDocumentSummary{}
	}
	return items
}

func nonNilSearchHits(items []KnowledgeSearchHit) []KnowledgeSearchHit {
	if items == nil {
		return []KnowledgeSearchHit{}
	}
	return items
}

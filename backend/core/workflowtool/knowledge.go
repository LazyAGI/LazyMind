package workflowtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"lazymind/core/workflow/executor"
)

const knowledgeBindingPath = "scripts/external_knowledge_tools.py"
const knowledgeBinding = "# LazyMind selected knowledge data adapters v1.\n# Executed only by Core's audited adapter; this module has no executable tools.\nADAPTER_ID = \"lazymind.knowledge.data.v1\"\n"

func knowledgeTool(name string) (Tool, bool) {
	properties := map[string]any{}
	required := []string{}
	description := "List documents only in the knowledge sources selected for this attempt; keyword matches document names without embeddings."
	switch name {
	case "knowledge_list_documents":
		properties["keyword"] = map[string]any{"type": "string", "maxLength": 200}
		properties["page_size"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}
		properties["page_token"] = map[string]any{"type": "string", "maxLength": 4096}
	case "knowledge_read_document":
		description = "Read existing text of a document in the fixed selected knowledge scope; never starts parsing, OCR, embeddings or a model."
		properties["document_id"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
		required = append(required, "document_id")
	default:
		return Tool{}, false
	}
	return Tool{Name: name, Source: "package", Available: true, Description: description,
		InputSchema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}, true
}

func (s *Service) validateKnowledgeBinding(ctx context.Context, contract executor.AttemptContext) error {
	var entry orm.WorkflowRevisionEntry
	if err := s.DB.WithContext(ctx).Where("revision_id = ? AND path = ? AND entry_type = ?", contract.WorkflowRevision, knowledgeBindingPath, "file").First(&entry).Error; err != nil || entry.BlobHash == nil {
		return &ToolError{Code: "TOOL_ADAPTER_UNAVAILABLE", Message: "The pinned package has no audited selected-knowledge adapter binding"}
	}
	var blob orm.WorkflowBlob
	if err := s.DB.WithContext(ctx).Where("hash = ?", *entry.BlobHash).First(&blob).Error; err != nil || string(blob.Content) != knowledgeBinding || fmt.Sprintf("%x", sha256.Sum256(blob.Content)) != *entry.BlobHash || entry.Binary || blob.Binary {
		return &ToolError{Code: "TOOL_NOT_ALLOWED", Message: "The selected-knowledge adapter binding does not match the audited version"}
	}
	return nil
}

func knowledgeArguments(arguments map[string]any, target any) error {
	raw, err := json.Marshal(arguments)
	if err != nil || len(raw) > 8192 {
		return &ToolError{Code: "INVALID_ARGUMENT", Message: "Knowledge tool arguments exceed the supported schema"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &ToolError{Code: "INVALID_ARGUMENT", Message: "Arguments do not match the selected-knowledge tool schema"}
	}
	return nil
}

func knowledgeCaller(ctx context.Context, owner string) doc.DatasetCatalogCaller {
	headers := dataToolCallerHeaders(ctx)
	return doc.DatasetCatalogCaller{UserID: owner, Authorization: headers.Get("Authorization"),
		TenantID: headers.Get("X-Tenant-Id"), UserRole: headers.Get("X-User-Role")}
}

func (s *Service) callKnowledge(ctx context.Context, owner string, contract executor.AttemptContext, name string, arguments map[string]any) (Result, error) {
	if err := s.validateKnowledgeBinding(ctx, contract); err != nil {
		return Result{}, err
	}
	if err := validateKnowledgeArguments(name, arguments); err != nil {
		return Result{}, err
	}
	scope := contract.KnowledgeScope
	if scope == nil || (len(scope.DatasetIDs) == 0 && len(scope.Documents) == 0) {
		return Result{Result: map[string]any{"status": "no_selected_knowledge", "documents": []any{},
			"message": "No knowledge sources were selected for this attempt. Use the bound attachments, or select a knowledge source before starting a new attempt."}}, nil
	}
	if len(scope.DatasetIDs)+len(scope.Documents) > 1000 {
		return Result{}, &ToolError{Code: "INVALID_SCOPE", Message: "The selected knowledge scope is too large"}
	}
	switch name {
	case "knowledge_list_documents":
		return s.listKnowledgeDocuments(ctx, owner, contract, arguments)
	case "knowledge_read_document":
		return s.readKnowledgeDocument(ctx, owner, contract, arguments)
	default:
		return Result{}, &ToolError{Code: "TOOL_NOT_ALLOWED", Message: "Unknown selected-knowledge tool"}
	}
}

type knowledgeListArguments struct {
	Keyword   string `json:"keyword"`
	PageSize  int    `json:"page_size"`
	PageToken string `json:"page_token"`
}

type knowledgeReadArguments struct {
	DocumentID string `json:"document_id"`
}

func validateKnowledgeArguments(name string, arguments map[string]any) error {
	switch name {
	case "knowledge_list_documents":
		var args knowledgeListArguments
		if err := knowledgeArguments(arguments, &args); err != nil {
			return err
		}
		_, explicitSize := arguments["page_size"]
		if len(args.Keyword) > 200 || (explicitSize && args.PageSize < 1) || args.PageSize > 100 || len(args.PageToken) > 4096 {
			return &ToolError{Code: "INVALID_ARGUMENT", Message: "Use a keyword up to 200 bytes and page_size 1..100"}
		}
	case "knowledge_read_document":
		var args knowledgeReadArguments
		if err := knowledgeArguments(arguments, &args); err != nil {
			return err
		}
		if strings.TrimSpace(args.DocumentID) == "" || len(args.DocumentID) > 128 {
			return &ToolError{Code: "INVALID_ARGUMENT", Message: "document_id is required and must be at most 128 bytes"}
		}
	default:
		return &ToolError{Code: "TOOL_NOT_ALLOWED", Message: "Unknown selected-knowledge tool"}
	}
	return nil
}

type knowledgePage struct {
	Attempt string `json:"attempt"`
	Scope   string `json:"scope"`
	Keyword string `json:"keyword"`
	After   string `json:"after"`
}

type knowledgeDocument struct {
	DocumentID      string `json:"document_id"`
	DatasetID       string `json:"dataset_id"`
	Name            string `json:"name"`
	ResourceVersion int64  `json:"resource_version"`
}

func (s *Service) listKnowledgeDocuments(ctx context.Context, owner string, contract executor.AttemptContext, arguments map[string]any) (Result, error) {
	var args knowledgeListArguments
	if err := knowledgeArguments(arguments, &args); err != nil {
		return Result{}, err
	}
	if args.PageSize == 0 {
		args.PageSize = 20
	}
	if len(args.Keyword) > 200 || args.PageSize < 1 || args.PageSize > 100 || len(args.PageToken) > 4096 {
		return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Use a keyword up to 200 bytes and page_size 1..100"}
	}
	rawScope, _ := json.Marshal(contract.KnowledgeScope)
	scopeHash := fmt.Sprintf("%x", sha256.Sum256(rawScope))
	page := knowledgePage{Attempt: contract.AttemptID, Scope: scopeHash, Keyword: args.Keyword}
	if args.PageToken != "" {
		raw, err := base64.RawURLEncoding.DecodeString(args.PageToken)
		if err != nil || json.Unmarshal(raw, &page) != nil || page.Attempt != contract.AttemptID || page.Scope != scopeHash || page.Keyword != args.Keyword {
			return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "The page token does not belong to this attempt, selection and keyword"}
		}
	}
	catalog, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: s.DB})
	if err != nil {
		return Result{}, err
	}
	allDatasets := map[string]bool{}
	for _, id := range contract.KnowledgeScope.DatasetIDs {
		allDatasets[id] = true
	}
	selectedDocuments := []string{}
	for _, item := range contract.KnowledgeScope.Documents {
		allDatasets[item.DatasetID] = true
		selectedDocuments = append(selectedDocuments, item.DocumentID)
	}
	for id := range allDatasets {
		if _, err := catalog.GetDatasetAccess(ctx, doc.DatasetGetRequest{UserID: owner, DatasetID: id, Caller: knowledgeCaller(ctx, owner)}); err != nil {
			return Result{}, &ToolError{Code: "KNOWLEDGE_UNAVAILABLE", Message: "A selected knowledge source is no longer readable; select an accessible source and start a new attempt"}
		}
	}
	query := s.DB.WithContext(ctx).Model(&orm.Document{}).Where("deleted_at IS NULL").Where("LOWER(document_type) <> ? OR document_type IS NULL", "folder")
	query = query.Where("dataset_id IN ? OR id IN ?", contract.KnowledgeScope.DatasetIDs, selectedDocuments)
	if args.Keyword != "" {
		// Escape LIKE metacharacters so the keyword remains a literal name match.
		keyword := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(args.Keyword))
		query = query.Where("LOWER(display_name) LIKE ? ESCAPE '!'", "%"+keyword+"%")
	}
	if page.After != "" {
		query = query.Where("id > ?", page.After)
	}
	var rows []orm.Document
	if err := query.Select("id", "dataset_id", "display_name", "resource_version").Order("id").Limit(args.PageSize + 1).Find(&rows).Error; err != nil {
		return Result{}, err
	}
	next := ""
	if len(rows) > args.PageSize {
		rows = rows[:args.PageSize]
		page.After = rows[len(rows)-1].ID
		raw, _ := json.Marshal(page)
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	items := make([]knowledgeDocument, 0, len(rows))
	for _, row := range rows {
		// A selected single document must not gain access by moving to another dataset.
		if !knowledgeDocumentAllowed(contract, row) {
			continue
		}
		items = append(items, knowledgeDocument{row.ID, row.DatasetID, row.DisplayName, row.ResourceVersion})
	}
	return Result{Result: map[string]any{"status": "ok", "documents": items, "next_page_token": next,
		"retrieval": "selected document names; no semantic or embedding search"}}, nil
}

func knowledgeDocumentAllowed(contract executor.AttemptContext, row orm.Document) bool {
	for _, id := range contract.KnowledgeScope.DatasetIDs {
		if row.DatasetID == id {
			return true
		}
	}
	for _, ref := range contract.KnowledgeScope.Documents {
		if ref.DocumentID == row.ID && ref.DatasetID == row.DatasetID {
			return true
		}
	}
	return false
}

func (s *Service) readKnowledgeDocument(ctx context.Context, owner string, contract executor.AttemptContext, arguments map[string]any) (Result, error) {
	var args knowledgeReadArguments
	if err := knowledgeArguments(arguments, &args); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(args.DocumentID) == "" || len(args.DocumentID) > 128 {
		return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "document_id is required and must be at most 128 bytes"}
	}
	var row orm.Document
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", args.DocumentID).First(&row).Error; err != nil || !knowledgeDocumentAllowed(contract, row) {
		return Result{}, &ToolError{Code: "KNOWLEDGE_SCOPE_DENIED", Message: "This document is not in the attempt's selected knowledge scope"}
	}
	for _, ref := range contract.KnowledgeScope.Documents {
		if ref.DocumentID == row.ID && ref.ResourceVersion > 0 && ref.ResourceVersion != row.ResourceVersion {
			return Result{}, &ToolError{Code: "SOURCE_VERSION_CHANGED", Message: "The bound document version changed; rebind the source before retrying"}
		}
	}
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: s.DB, LazyDB: store.LazyLLMDB()})
	if err != nil {
		return Result{}, err
	}
	content, err := service.ReadDocumentContent(ctx, doc.DocumentContentRequest{UserID: owner, DatasetID: row.DatasetID, DocumentID: row.ID, Caller: knowledgeCaller(ctx, owner)})
	if err != nil {
		return Result{}, &ToolError{Code: "DOCUMENT_CONTENT_UNAVAILABLE", Message: "The selected document is not readable; check its access and prepared content in LazyMind"}
	}
	if strings.TrimSpace(content.Text) == "" {
		return Result{}, &ToolError{Code: "DOCUMENT_CONTENT_UNAVAILABLE", Message: "No prepared text is available. Read the original with host capabilities, or configure document parsing in LazyMind and retry; this tool does not start OCR or a model."}
	}
	return Result{Result: map[string]any{"status": "ok", "document_id": row.ID, "dataset_id": row.DatasetID, "name": row.DisplayName, "resource_version": row.ResourceVersion,
		"content": map[string]any{"text": content.Text, "mime_type": content.MIMEType, "truncated": content.Truncated}}}, nil
}

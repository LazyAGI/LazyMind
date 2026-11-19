package workflowtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/academic"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
	"lazymind/core/workflow/executor"
)

const academicBindingPath = "scripts/external_academic_tools.py"
const academicBinding = "# LazyMind academic workflow data adapters v1.\n# Executed only by Core's audited adapter; this module has no executable tools.\nADAPTER_ID = \"lazymind.academic.data.v1\"\n"

// These reserved package adapters are data operations, not native generators or
// publishers. Their binding is exact, revision-owned, and never Python-executed.
func academicTool(name string) (Tool, bool) {
	text := func() map[string]any { return map[string]any{"type": "string"} }
	integer := func() map[string]any { return map[string]any{"type": "integer"} }
	stringsList := func() map[string]any { return map[string]any{"type": "array", "items": text()} }
	properties := map[string]any{}
	description := ""
	var required []string
	switch name {
	case "academic_context":
		description = "Read the current workflow conversation's owned academic context. No caller-selected task IDs."
	case "academic_expansion":
		description = "Read the bound expansion, registered candidates, confirmed directions, and genuine reading receipts."
	case "academic_search_papers":
		description = "Search and register real papers in this bound expansion. Continue using only a returned cursor; source configuration failures remain observable."
		properties = map[string]any{"query": text(), "sources": stringsList(), "mode": text(), "paper_id": text(), "max_results": integer(), "cursor": text()}
	case "academic_read_paper":
		description = "Read registered full text or reference clues in the current expansion or handoff; records delivered reading coverage. Content acquisition may be queued. Continue using only the returned cursor."
		properties = map[string]any{"paper_id": text(), "view": text(), "reference_keys": stringsList(), "keywords": stringsList(), "page_start": integer(), "page_end": integer(), "max_results": integer(), "positions": map[string]any{"type": "array", "items": integer()}, "cursor": text()}
	case "academic_resolve_papers":
		description = "Resolve registered paper identities and version-scoped content metadata from the bound expansion. Does not publish a generated report or import papers."
		properties, required = map[string]any{"paper_ids": stringsList()}, []string{"paper_ids"}
	default:
		return Tool{}, false
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if required != nil {
		schema["required"] = required
	}
	return Tool{Name: name, Source: "package", Description: description, InputSchema: schema, Available: true}, true
}

func (s *Service) validateAcademicBinding(ctx context.Context, contract executor.AttemptContext) error {
	var entry orm.WorkflowRevisionEntry
	var blob orm.WorkflowBlob
	if err := s.DB.WithContext(ctx).Where("revision_id = ? AND path = ? AND entry_type = ?", contract.WorkflowRevision, academicBindingPath, "file").Take(&entry).Error; err != nil || entry.BlobHash == nil || entry.Binary {
		return &ToolError{Code: "INVALID_PACKAGE", Message: "The pinned package lacks the audited academic data adapter binding"}
	}
	if err := s.DB.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		return &ToolError{Code: "INVALID_PACKAGE", Message: "The pinned academic adapter content is unavailable"}
	}
	digest := sha256.Sum256(blob.Content)
	if blob.Binary || hex.EncodeToString(digest[:]) != *entry.BlobHash || string(blob.Content) != academicBinding {
		return &ToolError{Code: "INVALID_PACKAGE", Message: "The pinned academic adapter does not match the audited version"}
	}
	return nil
}

type dataToolCallerKey struct{}

// Preserve the authenticated HTTP caller for dataset ACL/provider checks. These
// headers stay in-process and never enter a tool contract, receipt, or response.
func withDataToolCaller(ctx context.Context, source http.Header) context.Context {
	headers := make(http.Header)
	for _, name := range []string{"Authorization", "X-Tenant-Id", "X-User-Role"} {
		if value := source.Get(name); value != "" {
			headers.Set(name, value)
		}
	}
	return context.WithValue(ctx, dataToolCallerKey{}, headers)
}

func dataToolCallerHeaders(ctx context.Context) http.Header {
	if headers, ok := ctx.Value(dataToolCallerKey{}).(http.Header); ok {
		return headers.Clone()
	}
	return make(http.Header)
}

func academicBoundID(value any, depth int) string {
	if depth > 4 {
		return ""
	}
	switch v := value.(type) {
	case string:
		if len(v) <= 128 && strings.TrimSpace(v) == v {
			return v
		}
	case map[string]any:
		for _, key := range []string{"value", "text", "data"} {
			if nested, ok := v[key]; ok {
				return academicBoundID(nested, depth+1)
			}
		}
	}
	return ""
}

func (s *Service) academicInputID(ctx context.Context, owner string, contract executor.AttemptContext, material string) (string, bool, error) {
	value, supplied := contract.Inputs[material]
	if !supplied {
		return "", false, nil
	}
	if input, ok := value.(map[string]any); ok && input["source_type"] == "input_resource" {
		id, _ := input["source_id"].(string)
		revision, _ := input["source_revision"].(string)
		hash, _ := input["content_hash"].(string)
		var resource orm.WorkflowInputResource
		if id == "" || s.DB.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, owner).Take(&resource).Error != nil {
			return "", true, &ToolError{Code: "ACADEMIC_CONTEXT_UNAVAILABLE", Message: "The pinned academic input resource is unavailable to this owner"}
		}
		digest := sha256.Sum256(resource.Content)
		actual := "sha256:" + hex.EncodeToString(digest[:])
		if revision != strconv.FormatInt(resource.Revision, 10) || hash == "" || hash != resource.ContentHash || hash != actual {
			return "", true, &ToolError{Code: "INPUT_VERSION_CHANGED", Message: "The academic input resource does not match its pinned revision and content hash"}
		}
		mimeType := strings.TrimSpace(strings.SplitN(resource.MimeType, ";", 2)[0])
		if len(resource.Content) > 4096 || !utf8.Valid(resource.Content) || (!strings.HasPrefix(mimeType, "text/") && mimeType != "application/json") {
			return "", true, &ToolError{Code: "INVALID_ARGUMENT", Message: "An academic task input must be bounded UTF-8 text or JSON"}
		}
		if mimeType == "application/json" {
			if json.Unmarshal(resource.Content, &value) != nil {
				value = nil
			}
		} else {
			value = strings.TrimSpace(string(resource.Content))
		}
	}
	id := academicBoundID(value, 0)
	if id == "" {
		return "", true, &ToolError{Code: "ACADEMIC_CONTEXT_UNAVAILABLE", Message: "The pinned academic task input must contain its exact text value"}
	}
	return id, true, nil
}

// Scope comes from frozen inputs and the owning session, never from tool args.
func (s *Service) academicScope(ctx context.Context, owner string, contract executor.AttemptContext, name string) (string, string, string, error) {
	var session orm.WorkflowSession
	if err := s.DB.WithContext(ctx).Where("id = ? AND create_user_id = ?", contract.SessionID, owner).Take(&session).Error; err != nil {
		return "", "", "", err
	}
	var conversation orm.Conversation
	if session.ConversationID == "" || s.DB.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", session.ConversationID, owner).Take(&conversation).Error != nil {
		return "", "", "", &ToolError{Code: "ACADEMIC_CONTEXT_UNAVAILABLE", Message: "The workflow's owned academic conversation is unavailable"}
	}
	if name == "academic_context" {
		return session.ConversationID, "", "", nil
	}
	boundID, supplied, err := s.academicInputID(ctx, owner, contract, "expansion_id")
	if err != nil {
		return "", "", "", err
	}
	if supplied {
		var expansion orm.AcademicExpansion
		err := s.DB.WithContext(ctx).Where("id = ? AND conversation_id = ? AND user_id = ? AND deleted_at IS NULL", boundID, session.ConversationID, owner).Take(&expansion).Error
		if err == nil {
			return session.ConversationID, expansion.ID, "", nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", "", err
		}
		return "", "", "", &ToolError{Code: "ACADEMIC_CONTEXT_UNAVAILABLE", Message: "The fixed expansion does not belong to this workflow conversation"}
	}
	// Review/reflection workflows use a server-created handoff in this same Chat.
	if name == "academic_read_paper" {
		handoffID, fixed, err := s.academicInputID(ctx, owner, contract, "handoff_id")
		if err != nil {
			return "", "", "", err
		}
		query := s.DB.WithContext(ctx).Where("conversation_id = ? AND user_id = ?", session.ConversationID, owner)
		if fixed {
			query = query.Where("id = ?", handoffID)
		}
		var handoffs []orm.AcademicHandoff
		if err := query.Limit(2).Find(&handoffs).Error; err != nil {
			return "", "", "", err
		}
		if len(handoffs) == 1 {
			return session.ConversationID, "", handoffs[0].ID, nil
		}
		if len(handoffs) > 1 {
			return "", "", "", &ToolError{Code: "ACADEMIC_CONTEXT_AMBIGUOUS", Message: "Multiple academic handoffs exist in this conversation; a fixed handoff input is required"}
		}
	}
	return "", "", "", &ToolError{Code: "ACADEMIC_CONTEXT_UNAVAILABLE", Message: "No owned academic task matches the pinned workflow and conversation"}
}

func (s *Service) callAcademic(ctx context.Context, owner string, contract executor.AttemptContext, name string, arguments map[string]any) (Result, error) {
	tool, ok := academicTool(name)
	if !ok {
		return Result{}, &ToolError{Code: "TOOL_NOT_ALLOWED", Message: "Unknown academic data adapter"}
	}
	if err := s.validateAcademicBinding(ctx, contract); err != nil {
		return Result{}, err
	}
	// The reused HTTP handlers use Core's configured store. Never authorize in
	// one database and execute the data operation against another database.
	if corestore.DB() == nil {
		return Result{}, unavailable("The academic data store is unavailable")
	}
	workflowDB, workflowErr := s.DB.DB()
	academicDB, academicErr := corestore.DB().DB()
	if workflowErr != nil || academicErr != nil || workflowDB != academicDB {
		return Result{}, unavailable("The academic data store does not match the workflow store")
	}
	properties := tool.InputSchema["properties"].(map[string]any)
	payload := map[string]any{}
	for key, value := range arguments {
		schema, ok := properties[key].(map[string]any)
		if !ok {
			return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Unknown argument or caller-selected academic context is not allowed"}
		}
		if !academicArgumentMatches(value, schema) {
			return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Academic tool arguments do not match their declared types"}
		}
		payload[key] = value
	}
	if required, ok := tool.InputSchema["required"].([]string); ok {
		for _, key := range required {
			if _, exists := payload[key]; !exists {
				return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "A required academic tool argument is missing"}
			}
		}
	}
	if cursor, ok := payload["cursor"]; ok && cursor != "" && len(payload) != 1 {
		return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Continue an academic result with its cursor only"}
	}
	conversation, expansion, handoff, err := s.academicScope(ctx, owner, contract, name)
	if err != nil {
		return Result{}, err
	}
	method, target := http.MethodPost, "/academic/"
	var handler http.HandlerFunc
	switch name {
	case "academic_context":
		method, target, handler = http.MethodGet, "/academic/chat-context?conversation_id="+url.QueryEscape(conversation), academic.AcademicChatContext
	case "academic_expansion":
		method, handler = http.MethodGet, academic.GetExpansion
	case "academic_search_papers":
		payload["expansion_id"], handler = expansion, academic.SearchPapers
	case "academic_read_paper":
		if expansion != "" {
			payload["expansion_id"] = expansion
		} else {
			payload["handoff_id"] = handoff
		}
		handler = academic.ReadPaperContent
	case "academic_resolve_papers":
		handler = academic.ResolveExpansionPapers
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > 2<<20 {
		return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Academic tool arguments must be bounded JSON"}
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	request.Header = dataToolCallerHeaders(ctx)
	request.Header.Set("X-User-Id", owner)
	request.Header.Set("Content-Type", "application/json")
	request = mux.SetURLVars(request, map[string]string{"expansion_id": expansion})
	response := &academicResponse{headers: make(http.Header), status: http.StatusOK}
	handler(response, request)
	if response.overflow {
		return Result{}, &ToolError{Code: "TOOL_RESULT_TOO_LARGE", Message: "Academic tool result exceeds the supported resource transport limit"}
	}
	if response.status < 200 || response.status >= 300 {
		var failure struct{ Code, Message string }
		if json.Unmarshal(response.body.Bytes(), &failure) != nil || failure.Code == "" {
			return Result{}, unavailable("The academic data service is unavailable")
		}
		return Result{}, &ToolError{Code: failure.Code, Message: failure.Message, Retryable: response.status >= 500}
	}
	var value any
	if err := json.Unmarshal(response.body.Bytes(), &value); err != nil {
		return Result{}, err
	}
	if name == "academic_search_papers" {
		if result, ok := value.(map[string]any); ok {
			var missing []string
			if sources, ok := result["sources"].([]any); ok {
				for _, raw := range sources {
					if source, ok := raw.(map[string]any); ok && (source["status"] == "missing_configuration" || source["status"] == "authentication_failed") {
						if name, ok := source["source"].(string); ok {
							missing = append(missing, name)
						}
					}
				}
			}
			if len(missing) != 0 {
				result["configuration_required"] = missing
				result["retry_guidance"] = "Ask the user to configure or repair these source capabilities in LazyMind, then retry the affected step. Preserve partial results and never invent missing evidence."
			}
		}
	}
	return Result{Result: value}, nil
}

func academicArgumentMatches(value any, schema map[string]any) bool {
	// Normalize direct Go and HTTP JSON callers to the same primitive types.
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch schema["type"] {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && math.Trunc(number) == number
	case "array":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		itemSchema := schema["items"].(map[string]any)
		for _, item := range items {
			if !academicArgumentMatches(item, itemSchema) {
				return false
			}
		}
		return true
	}
	return false
}

type academicResponse struct {
	headers  http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (r *academicResponse) Header() http.Header    { return r.headers }
func (r *academicResponse) WriteHeader(status int) { r.status = status }
func (r *academicResponse) Write(value []byte) (int, error) {
	if r.body.Len()+len(value) > maxResultBytes {
		r.overflow = true
		return 0, io.ErrShortBuffer
	}
	return r.body.Write(value)
}

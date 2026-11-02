package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// SiliconFlow's documented filters, in stable order. There is no documented
// speech-synthesis filter: the unfiltered list must remain the complete baseline.
// https://api-docs.siliconflow.cn/docs/api/models-get
var siliconFlowModelSubTypes = [...]string{
	"chat", "embedding", "reranker", "text-to-image", "image-to-image", "speech-to-text", "text-to-video",
}

const remoteModelsResponseLimit = 8 << 20

type remoteModelsRequestError struct {
	status int
	reason string
	cause  error
}

func (err remoteModelsRequestError) Error() string {
	if err.status != 0 {
		return fmt.Sprintf("list remote models failed: HTTP %d", err.status)
	}
	return "list remote models failed"
}

func (err remoteModelsRequestError) FailureCode() string { return err.reason }
func (err remoteModelsRequestError) Unwrap() error       { return err.cause }

// fetchRemoteModelEntries shares the configured safe transport for ordinary and
// category-specific discovery. Only an allowlisted reason survives provider IO.
func fetchRemoteModelEntries(req *http.Request) ([]openaiModelListEntry, error) {
	response, err := remoteModelsHTTPClient.Do(req)
	if err != nil {
		return nil, remoteModelsRequestError{reason: FailureCodeForRemoteModels(0, nil, err), cause: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, remoteModelsResponseLimit+1))
	if err != nil {
		return nil, remoteModelsRequestError{status: response.StatusCode, reason: FailureCodeForRemoteModels(response.StatusCode, nil, err), cause: err}
	}
	if len(body) > remoteModelsResponseLimit {
		return nil, remoteModelsRequestError{status: response.StatusCode, reason: "invalid_response"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, remoteModelsRequestError{status: response.StatusCode, reason: FailureCodeForRemoteModels(response.StatusCode, body, nil)}
	}
	if !json.Valid(body) {
		return nil, remoteModelsRequestError{status: response.StatusCode, reason: "invalid_response"}
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(body, &shape) == nil {
		semanticFailure := false
		if raw := bytes.TrimSpace(shape["error"]); len(raw) > 0 && string(raw) != "null" {
			semanticFailure = true
		}
		var code any
		if raw, ok := shape["code"]; ok && json.Unmarshal(raw, &code) == nil {
			semanticFailure = semanticFailure || (code != nil && code != float64(0) && code != "0" && code != "")
		}
		if semanticFailure {
			reason := FailureCodeForRemoteModels(response.StatusCode, body, nil)
			if reason == "invalid_response" {
				reason = "upstream_error"
			}
			return nil, remoteModelsRequestError{status: response.StatusCode, reason: reason}
		}
		foundArray := false
		for _, field := range []string{"data", "models"} {
			raw, found := shape[field]
			if !found || string(bytes.TrimSpace(raw)) == "null" {
				continue
			}
			var entries []openaiModelListEntry
			if json.Unmarshal(raw, &entries) != nil || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
				return nil, remoteModelsRequestError{status: response.StatusCode, reason: "invalid_response"}
			}
			foundArray = true
		}
		if !foundArray {
			return nil, remoteModelsRequestError{status: response.StatusCode, reason: "invalid_response"}
		}
	} else {
		var legacyIDs []string
		if json.Unmarshal(body, &legacyIDs) != nil || !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			return nil, remoteModelsRequestError{status: response.StatusCode, reason: "invalid_response"}
		}
	}
	return parseRemoteModelEntries(body), nil
}

func isSiliconFlowSupplier(ctx context.Context, db *gorm.DB, parent orm.UserModelProvider) bool {
	protocol := LazyLLMSource(RuntimeProviderName(parent))
	if protocol != "openai" && protocol != "siliconflow" {
		return false
	}
	if protocol == "siliconflow" || (!parent.IsCustom && normalizeProviderName(parent.Name) == "siliconflow") {
		return true
	}
	if db == nil || parent.DefaultModelProviderID == "" || !db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		return false
	}
	var template orm.DefaultModelProvider
	if db.WithContext(ctx).Select("name").Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).Take(&template).Error != nil {
		return false
	}
	return normalizeProviderName(template.Name) == "siliconflow"
}

// listSiliconFlowModelCategories enriches the full baseline within its request's
// shared deadline. Unsupported filters and transient failures never erase it.
func listSiliconFlowModelCategories(request *http.Request, baseline []openaiModelListEntry) ([]openaiModelListEntry, error) {
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	type result struct {
		entries []openaiModelListEntry
		err     error
	}
	results := make([]result, len(siliconFlowModelSubTypes))
	jobs := make(chan int, len(siliconFlowModelSubTypes))
	for i := range siliconFlowModelSubTypes {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range 3 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				req := request.Clone(ctx)
				query := req.URL.Query()
				query.Set("sub_type", siliconFlowModelSubTypes[index])
				req.URL.RawQuery = query.Encode()
				results[index].entries, results[index].err = fetchRemoteModelEntries(req)
				var failure remoteModelsRequestError
				if errors.As(results[index].err, &failure) && (failure.status == http.StatusUnauthorized || failure.status == http.StatusForbidden) {
					cancel()
					return
				}
			}
		}()
	}
	workers.Wait()
	for _, item := range results {
		var failure remoteModelsRequestError
		if errors.As(item.err, &failure) && (failure.status == http.StatusUnauthorized || failure.status == http.StatusForbidden) {
			return nil, item.err
		}
	}
	byID := make(map[string]openaiModelListEntry, len(baseline))
	for _, entry := range baseline {
		byID[strings.ToLower(entry.ID)] = entry
	}
	for index, result := range results {
		if result.err != nil {
			continue
		}
		for _, incoming := range result.entries {
			key := strings.ToLower(incoming.ID)
			existing, found := byID[key]
			if !found {
				existing = incoming
			} else {
				existing = mergeSiliconFlowModelMetadata(existing, incoming)
			}
			existing.DiscoverySubTypes = append(existing.DiscoverySubTypes, siliconFlowModelSubTypes[index])
			byID[key] = existing
		}
	}
	entries := make([]openaiModelListEntry, 0, len(byID))
	for _, entry := range byID {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].ID) < strings.ToLower(entries[j].ID) })
	return entries, nil
}

// Keep all raw modality evidence. A later ID-only category response must not
// discard metadata supplied by the unfiltered list or another category.
func mergeSiliconFlowModelMetadata(existing, incoming openaiModelListEntry) openaiModelListEntry {
	union := func(first, second []string) []string {
		out := append([]string(nil), first...)
		seen := modalitySet(first)
		for _, value := range second {
			key := strings.ToLower(strings.TrimSpace(value))
			if !seen[key] {
				out = append(out, value)
				seen[key] = true
			}
		}
		return out
	}
	existing.Architecture.Input = union(union(existing.Architecture.Input, existing.InputModalities), union(incoming.Architecture.Input, incoming.InputModalities))
	existing.Architecture.Output = union(union(existing.Architecture.Output, existing.OutputModalities), union(incoming.Architecture.Output, incoming.OutputModalities))
	if existing.ContextLength <= 0 {
		existing.ContextLength = incoming.ContextLength
	}
	if existing.ModelType == "" {
		existing.ModelType = incoming.ModelType
	}
	if existing.TaskType == "" {
		existing.TaskType = incoming.TaskType
	}
	if existing.Task == "" {
		existing.Task = incoming.Task
	}
	return existing
}

func siliconFlowDiscoveredModelType(entry openaiModelListEntry, input, output []string, catalogType string) string {
	if len(entry.DiscoverySubTypes) != 1 {
		return ""
	}
	switch entry.DiscoverySubTypes[0] {
	case "chat":
		if len(output) > 0 {
			modalities := modalitySet(output)
			if len(modalities) != 1 || !modalities["text"] {
				return ""
			}
		}
		return classifyUpstreamModel("llm", input, output)
	case "embedding":
		var inputs []string
		for _, value := range input {
			if value = strings.TrimSpace(value); value != "" {
				inputs = append(inputs, value)
			}
		}
		if len(inputs) > 0 {
			return classifyUpstreamModel("embed", inputs, output)
		}
		if entry.ModelType == "embed" || entry.ModelType == "multimodal_embedding" {
			return entry.ModelType
		}
		if catalogType == "embed" || catalogType == "multimodal_embedding" {
			return catalogType
		}
		return ""
	case "reranker":
		return "rerank"
	case "text-to-image":
		return "text2image"
	case "image-to-image":
		return "image_editing"
	case "speech-to-text":
		return "stt"
	case "text-to-video":
		return "text2video"
	}
	return ""
}

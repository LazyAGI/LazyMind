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
	"lazymind/core/workflow/executor"
)

const preparePPTPage = "ppt_prepare_page"

// Resolve the brief by the requested visual page number. The external model
// cannot substitute an edited or incomplete brief for the reviewed revision.
func (s *Service) bindPPTPageArguments(ctx context.Context, contract executor.AttemptContext, name string, arguments map[string]any) (map[string]any, *executor.PPTPageImages, error) {
	if name != preparePPTPage || !executor.UsesPPTImageContract(contract) {
		return arguments, nil, nil
	}
	var page int
	raw, err := json.Marshal(arguments["page_no"])
	if err != nil || json.Unmarshal(raw, &page) != nil || page < 1 || page > 100 {
		return nil, nil, &ToolError{Code: "INVALID_ARGUMENT", Message: "page_no must be between 1 and 100"}
	}
	binding, err := executor.LoadPPTPageImages(s.DB.WithContext(ctx), contract, page)
	if err != nil {
		return nil, nil, &ToolError{Code: "INVALID_ARGUMENT", Message: err.Error()}
	}
	resolved := make(map[string]any, len(arguments))
	for key, value := range arguments {
		resolved[key] = value
	}
	resolved["brief"] = binding.GenerationBrief()
	return resolved, &binding, nil
}

func withPPTPageSchema(contract executor.AttemptContext, tool Tool) Tool {
	if tool.Name != preparePPTPage || !executor.UsesPPTImageContract(contract) || tool.InputSchema == nil {
		return tool
	}
	properties, _ := tool.InputSchema["properties"].(map[string]any)
	delete(properties, "brief")
	tool.InputSchema["required"] = []string{"page_no"}
	tool.Description = "Prepare one approved page using its pinned brief and image bindings. First pass page_no; " +
		"then pass the same page_no with the requested model response. Use the returned HTML file with embedded images for rendering, export and publication."
	return tool
}

func (s *Service) finishPPTPage(ctx context.Context, binding *executor.PPTPageImages, result *Result) error {
	if binding == nil || result.Error != nil {
		return nil
	}
	value, _ := result.Result.(map[string]any)
	if value["status"] == "generation_required" {
		return nil
	}
	files, _ := value["files"].([]any)
	if value["status"] != "ready" || len(files) != 1 {
		return unavailable("The PPT page adapter returned an invalid result")
	}
	file, _ := files[0].(map[string]any)
	encoded, _ := file["content_base64"].(string)
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return unavailable("The PPT page adapter returned an invalid file")
	}
	html, err := binding.BindHTML(s.DB.WithContext(ctx), string(data))
	if err != nil {
		return &ToolError{Code: "INVALID_ARGUMENT", Message: err.Error()}
	}
	file["content_base64"] = base64.StdEncoding.EncodeToString([]byte(html))
	file["sha256"] = fmt.Sprintf("%x", sha256.Sum256([]byte(html)))
	file["size_bytes"] = len(html)
	return nil
}

// Small argument objects for the audited download tool.
func decodePPTToolArgs(arguments map[string]any, dest any) error {
	raw, err := json.Marshal(arguments)
	if err != nil || len(raw) > 16<<10 {
		return &ToolError{Code: "INVALID_ARGUMENT", Message: "PPT image tool arguments are too large"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dest) != nil {
		return &ToolError{Code: "INVALID_ARGUMENT", Message: "Arguments do not match the PPT image tool schema"}
	}
	return nil
}

func pptImageFile(data []byte, mime string) map[string]any {
	name := "material." + strings.TrimPrefix(mime, "image/")
	return map[string]any{"name": name, "mime_type": mime, "content_base64": base64.StdEncoding.EncodeToString(data),
		"size_bytes": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data))}
}

const downloadPPTImage = "ppt_download_material_image"
const pptImageBindingPath = "scripts/external_image_tools.py"
const pptImageBinding = "# LazyMind PPT material image download adapter.\nADAPTER_ID = 'lazymind.ppt.material-image.v1'\n"

func pptImageDownloadTool() Tool {
	return Tool{Name: downloadPPTImage, Source: "package", Available: true,
		Description: "Download and verify a raster image from direct public search URLs in preference order. " +
			"Returns the first usable file or the failures for each candidate. Inspect the saved file and publish it with output.local_path.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"urls"},
			"properties": map[string]any{"urls": map[string]any{"type": "array", "minItems": 1, "maxItems": 5,
				"items": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048}}}}}
}

func (s *Service) validatePPTImageBinding(ctx context.Context, contract executor.AttemptContext) error {
	db := s.DB.WithContext(ctx)
	var entry orm.WorkflowRevisionEntry
	if err := db.Where("revision_id = ? AND path = ? AND entry_type = ?", contract.WorkflowRevision, pptImageBindingPath, "file").Take(&entry).Error; err != nil || entry.BlobHash == nil {
		return &ToolError{Code: "TOOL_ADAPTER_UNAVAILABLE", Message: "The pinned package has no audited PPT image download binding"}
	}
	var blob orm.WorkflowBlob
	if err := db.Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil || string(blob.Content) != pptImageBinding ||
		fmt.Sprintf("%x", sha256.Sum256(blob.Content)) != *entry.BlobHash || entry.Binary || blob.Binary {
		return &ToolError{Code: "TOOL_NOT_ALLOWED", Message: "The pinned PPT image download binding does not match the audited version"}
	}
	return nil
}

func callPPTImageDownload(ctx context.Context, arguments map[string]any, download func(context.Context, string, int64) ([]byte, error)) (Result, error) {
	var input struct {
		URLs []string `json:"urls"`
	}
	if err := decodePPTToolArgs(arguments, &input); err != nil {
		return Result{}, err
	}
	if len(input.URLs) == 0 || len(input.URLs) > 5 {
		return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Supply one to five direct image URLs"}
	}
	for _, source := range input.URLs {
		if source == "" || len(source) > 2048 {
			return Result{}, &ToolError{Code: "INVALID_ARGUMENT", Message: "Image URLs must be nonempty and within 2048 characters"}
		}
	}
	failed := []map[string]any{}
	seen := map[string]bool{}
	for i, source := range input.URLs {
		if seen[source] {
			continue
		}
		seen[source] = true
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		data, err := download(ctx, source, executor.MaxPPTImageBytes)
		if err == nil {
			var mime string
			mime, err = executor.PPTImageMIME(data)
			if err == nil {
				return Result{Result: map[string]any{"status": "ready", "source_url": source, "failures": failed,
					"files": []map[string]any{pptImageFile(data, mime)}}}, nil
			}
		}
		failed = append(failed, map[string]any{"candidate": i + 1, "reason": err.Error()})
	}
	return Result{Result: map[string]any{"status": "unavailable", "failures": failed}}, nil
}

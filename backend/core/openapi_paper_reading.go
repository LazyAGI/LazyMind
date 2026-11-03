package main

func paperReadingOpenAPISchemas() map[string]any {
	hash := map[string]any{"type": "string", "pattern": "^[a-fA-F0-9]{64}$", "description": "Original PDF SHA-256; a supplied request hash must match the current source or the server returns409 before cache/model access."}
	ids := array(strSchema())
	evidence := array(refSchema("PDFFormulaEvidence"))
	return map[string]any{
		"ReadingPreparationPolicy":     objReq([]string{"preset", "modes", "limits"}, prop("preset", enumStringSchema("demand", "advance", "full", "custom")), prop("modes", map[string]any{"type": "object", "additionalProperties": enumStringSchema("on_demand", "automatic", "disabled")}), prop("limits", objReq([]string{"daily_documents", "study_items_per_document", "automatic_concurrency"}, prop("daily_documents", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}), prop("study_items_per_document", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}), prop("automatic_concurrency", map[string]any{"type": "integer", "enum": []int{1}}))), prop("automatic_eligible_after", strSchema())),
		"ReadingPreparationRequest":    objReq([]string{"scopes"}, prop("scopes", array(enumStringSchema("pdf_parse", "formula", "figure", "table")))),
		"ReadingPreparationStatus":     objReq([]string{"status"}, prop("job_id", strSchema()), prop("status", enumStringSchema("not_requested", "pending", "running", "succeeded", "failed", "canceled")), prop("progress_current", map[string]any{"type": "integer"}), prop("progress_total", map[string]any{"type": "integer"}), prop("model_requests", map[string]any{"type": "integer"}), prop("error_message", strSchema()), prop("scopes", array(strSchema()))),
		"PDFPaperReadingRequest":       obj(prop("language", enumStringSchema("zh", "en")), prop("force", boolSchema()), prop("source_sha256", hash)),
		"PDFVisualExplanation":         objReq([]string{"summary", "observations", "evidence", "model", "warnings", "source_sha256"}, prop("summary", strSchema()), prop("observations", array(objReq([]string{"text", "evidence_ids", "inferred"}, prop("text", strSchema()), prop("evidence_ids", ids), prop("inferred", boolSchema())))), prop("evidence", evidence), prop("model", strSchema()), prop("warnings", array(strSchema())), prop("source_sha256", hash)),
		"PDFBlockTranslation":          objReq([]string{"block_id", "translated_text", "language", "model", "source_sha256"}, prop("block_id", strSchema()), prop("translated_text", strSchema()), prop("language", enumStringSchema("zh", "en")), prop("model", strSchema()), prop("source_sha256", hash)),
		"PDFVisualExplanationEnvelope": objReq([]string{"data"}, prop("data", refSchema("PDFVisualExplanation"))),
		"PDFBlockTranslationEnvelope":  objReq([]string{"data"}, prop("data", refSchema("PDFBlockTranslation"))),
	}
}
func paperReadingOpenAPIPaths() map[string]any {
	readingOperation := func(summary, schema string, body bool) map[string]any {
		responseSchema := schema
		if schema == "ReadingPreparationRequest" {
			responseSchema = "ReadingPreparationStatus"
		}
		op := map[string]any{"summary": summary, "responses": map[string]any{"200": response(200, "Personal policy or durable preparation status", obj(prop("data", refSchema(responseSchema)))), "400": response(400, "Invalid policy, dependency or limits", refSchema("ErrorResponse")), "403": response(403, "Generation prohibited by personal policy", refSchema("ErrorResponse"))}}
		if body {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": refSchema(schema)}}}
		}
		return op
	}

	operation := func(summary, schema string, block bool) map[string]any {
		op := map[string]any{"summary": summary, "description": "Requires document read permission and current source-matched PDF IR. Uses only the selected chat model, without retrieval. Optional source_sha256 binds the request to the displayed PDF and rejects stale sources with409; every response includes the authoritative source_sha256. Language defaults to zh; force refreshes the user/document/source/parser/model/language cache. Evidence quotes and zero-based page coordinates come only from the source IR. Figure/table explanations are contextual: no image pixels are supplied. Paragraph translation accepts only authoritative textual blocks up to 8000 Unicode characters.", "requestBody": map[string]any{"required": false, "content": map[string]any{"application/json": map[string]any{"schema": refSchema("PDFPaperReadingRequest")}}}, "responses": map[string]any{"200": response(200, "Source-grounded reading result", refSchema(schema)), "400": response(400, "Invalid request", refSchema("ErrorResponse")), "404": response(404, "Missing or forbidden document/block", refSchema("ErrorResponse")), "409": response(409, "PDF structure missing or source changed", refSchema("ErrorResponse")), "422": response(422, "Model unavailable or source block too large", refSchema("ErrorResponse")), "502": response(502, "Model request failed or response invalid", refSchema("ErrorResponse"))}}
		if block {
			op["parameters"] = []any{map[string]any{"name": "block", "in": "path", "required": true, "schema": strSchema()}}
		}
		return map[string]any{"post": op}
	}
	return map[string]any{
		"/user/reading-preferences":                                                  map[string]any{"get": readingOperation("Read personal AI reading preferences and capability registry", "ReadingPreparationPolicy", false), "put": readingOperation("Replace personal preferences; never process historical documents", "ReadingPreparationPolicy", true)},
		"/datasets/{dataset}/documents/{document}:prepare-reading":                   map[string]any{"post": readingOperation("Explicitly prepare this document; scopes only; preserves existing outputs", "ReadingPreparationRequest", true)},
		"/datasets/{dataset}/documents/{document}/reading-preparation":               map[string]any{"get": readingOperation("Read durable preparation progress and accounted model requests", "ReadingPreparationStatus", false)},
		"/datasets/{dataset}/documents/{document}/parse-status":                      map[string]any{"get": map[string]any{"summary": "Observe chat parsing without submitting new work", "responses": map[string]any{"200": response(200, "Observed parse state", obj(prop("status", enumStringSchema("not_requested", "parsed", "parsing", "failed", "unknown")), prop("task_id", strSchema()))), "202": response(202, "Existing parsing in progress", obj(prop("status", strSchema())))}}},
		"/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/explanation": operation("Explain a figure or table using extracted text and cells", "PDFVisualExplanationEnvelope", true),
		"/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/translation": operation("Translate an authoritative original PDF paragraph", "PDFBlockTranslationEnvelope", true),
	}
}

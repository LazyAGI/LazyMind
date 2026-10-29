package main

func paperReadingOpenAPISchemas() map[string]any {
	hash := map[string]any{"type": "string", "pattern": "^[a-fA-F0-9]{64}$", "description": "Original PDF SHA-256; a supplied request hash must match the current source or the server returns409 before cache/model access."}
	ids := array(strSchema())
	evidence := array(refSchema("PDFFormulaEvidence"))
	stage := objReq([]string{"key", "title", "summary", "evidence_ids", "related_block_ids", "inferred"}, prop("key", enumStringSchema("problem", "insight", "method", "evidence", "limitations")), prop("title", strSchema()), prop("summary", strSchema()), prop("evidence_ids", ids), prop("related_block_ids", ids), prop("inferred", boolSchema()))
	return map[string]any{
		"PDFPaperReadingRequest":       obj(prop("language", enumStringSchema("zh", "en")), prop("force", boolSchema()), prop("source_sha256", hash)),
		"PDFPaperGuideStage":           stage,
		"PDFPaperGuide":                objReq([]string{"stages", "evidence", "model", "warnings", "source_sha256"}, prop("stages", map[string]any{"type": "array", "minItems": 5, "maxItems": 5, "items": refSchema("PDFPaperGuideStage")}), prop("evidence", evidence), prop("model", strSchema()), prop("warnings", array(strSchema())), prop("source_sha256", hash)),
		"PDFVisualExplanation":         objReq([]string{"summary", "observations", "evidence", "model", "warnings", "source_sha256"}, prop("summary", strSchema()), prop("observations", array(objReq([]string{"text", "evidence_ids", "inferred"}, prop("text", strSchema()), prop("evidence_ids", ids), prop("inferred", boolSchema())))), prop("evidence", evidence), prop("model", strSchema()), prop("warnings", array(strSchema())), prop("source_sha256", hash)),
		"PDFBlockTranslation":          objReq([]string{"block_id", "translated_text", "language", "model", "source_sha256"}, prop("block_id", strSchema()), prop("translated_text", strSchema()), prop("language", enumStringSchema("zh", "en")), prop("model", strSchema()), prop("source_sha256", hash)),
		"PDFPaperGuideEnvelope":        objReq([]string{"data"}, prop("data", refSchema("PDFPaperGuide"))),
		"PDFVisualExplanationEnvelope": objReq([]string{"data"}, prop("data", refSchema("PDFVisualExplanation"))),
		"PDFBlockTranslationEnvelope":  objReq([]string{"data"}, prop("data", refSchema("PDFBlockTranslation"))),
	}
}
func paperReadingOpenAPIPaths() map[string]any {
	operation := func(summary, schema string, block bool) map[string]any {
		op := map[string]any{"summary": summary, "description": "Requires document read permission and current source-matched PDF IR. Uses only the selected chat model, without retrieval. Optional source_sha256 binds the request to the displayed PDF and rejects stale sources with409; every response includes the authoritative source_sha256. Language defaults to zh; force refreshes the user/document/source/parser/model/language cache. Evidence quotes and zero-based page coordinates come only from the source IR. Guides contain exactly five stages. Figure/table explanations are contextual: no image pixels are supplied. Paragraph translation accepts only authoritative textual blocks up to 8000 Unicode characters.", "requestBody": map[string]any{"required": false, "content": map[string]any{"application/json": map[string]any{"schema": refSchema("PDFPaperReadingRequest")}}}, "responses": map[string]any{"200": response(200, "Source-grounded reading result", refSchema(schema)), "400": response(400, "Invalid request", refSchema("ErrorResponse")), "404": response(404, "Missing or forbidden document/block", refSchema("ErrorResponse")), "409": response(409, "PDF structure missing or source changed", refSchema("ErrorResponse")), "422": response(422, "Model unavailable or source block too large", refSchema("ErrorResponse")), "502": response(502, "Model request failed or response invalid", refSchema("ErrorResponse"))}}
		if block {
			op["parameters"] = []any{map[string]any{"name": "block", "in": "path", "required": true, "schema": strSchema()}}
		}
		return map[string]any{"post": op}
	}
	return map[string]any{
		"/datasets/{dataset}/documents/{document}/pdf-ir/guide":                      operation("Create a five-stage paper guide from bounded source evidence", "PDFPaperGuideEnvelope", false),
		"/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/explanation": operation("Explain a figure or table using extracted text and cells", "PDFVisualExplanationEnvelope", true),
		"/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/translation": operation("Translate an authoritative original PDF paragraph", "PDFBlockTranslationEnvelope", true),
	}
}

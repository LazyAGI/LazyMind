package main

func formulaExplanationOpenAPISchemas() map[string]any {
	ids := array(strSchema())
	bbox := map[string]any{"type": "array", "minItems": 4, "maxItems": 4, "items": map[string]any{"type": "number", "minimum": 0, "maximum": 1}}
	return map[string]any{
		"PDFFormulaExplanationRequest":  obj(prop("force", boolSchema()), prop("language", enumStringSchema("zh", "en")), prop("focus_symbol", map[string]any{"type": "string", "minLength": 1, "maxLength": 500, "description": "Optional nonblank exact substring of the target formula LaTeX; requests a focused symbol definition with a separate cache entry."})),
		"PDFFormulaEvidence":            objReq([]string{"id", "text", "page"}, prop("id", strSchema()), prop("text", strSchema()), prop("page", map[string]any{"type": "integer", "minimum": 0}), prop("bbox", bbox)),
		"PDFFormulaExplanation":         objReq([]string{"summary", "symbols", "steps", "evidence", "warnings"}, prop("summary", strSchema()), prop("symbols", array(objReq([]string{"symbol", "meaning", "evidence_ids", "inferred"}, prop("symbol", strSchema()), prop("meaning", strSchema()), prop("evidence_ids", ids), prop("inferred", boolSchema())))), prop("steps", array(objReq([]string{"text", "evidence_ids", "inferred"}, prop("text", strSchema()), prop("evidence_ids", ids), prop("inferred", boolSchema())))), prop("evidence", array(refSchema("PDFFormulaEvidence"))), prop("warnings", array(strSchema())), prop("model", strSchema())),
		"PDFFormulaExplanationEnvelope": objReq([]string{"data"}, prop("data", refSchema("PDFFormulaExplanation"))),
	}
}
func formulaExplanationOpenAPIPaths() map[string]any {
	return map[string]any{"/datasets/{dataset}/documents/{document}/pdf-ir/formulas/{block}/explanation": map[string]any{"post": map[string]any{
		"summary":     "Explain a formula from original PDF evidence using the selected chat model without retrieval",
		"description": "Requires document read permission and current PDF IR. Source pages are zero based and bounding boxes use normalized visible-page coordinates. The backend supplies verbatim evidence; model paraphrases and derivations are marked inferred. Language defaults to zh. focus_symbol optionally requests the exact clicked symbol; it must be a nonblank substring of the current formula (at most 500 Unicode characters). force refreshes the source/model/language/focus-specific cache.",
		"parameters":  []any{map[string]any{"name": "block", "in": "path", "required": true, "schema": strSchema(), "description": "Formula block id from the current PDF IR"}},
		"requestBody": map[string]any{"required": false, "content": map[string]any{"application/json": map[string]any{"schema": refSchema("PDFFormulaExplanationRequest")}}},
		"responses":   map[string]any{"200": response(200, "Formula explanation with source evidence", refSchema("PDFFormulaExplanationEnvelope")), "400": response(400, "Invalid request", refSchema("ErrorResponse")), "404": response(404, "Document or formula missing or forbidden", refSchema("ErrorResponse")), "409": response(409, "Parse or refresh the original PDF first", refSchema("ErrorResponse")), "422": response(422, "Selected chat model unavailable or formula too large", refSchema("ErrorResponse")), "502": response(502, "Model generation failed or invalid output", refSchema("ErrorResponse"))},
	}}}
}

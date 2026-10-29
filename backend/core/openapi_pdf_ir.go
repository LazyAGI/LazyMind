package main

func pdfIROpenAPISchemas() map[string]any {
	bbox := map[string]any{"type": "array", "minItems": 4, "maxItems": 4, "items": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "description": "[left, top, right, bottom], normalized to the original visible page with intrinsic rotation"}
	block := objReq([]string{"id", "type", "page"}, prop("id", strSchema()), prop("type", strSchema()), prop("page", map[string]any{"type": "integer", "minimum": 0}), prop("bbox", bbox), prop("latex", strSchema()), prop("text", strSchema()))
	document := objReq([]string{"version", "source_sha256", "parser", "coordinate_space", "pages", "blocks"}, prop("version", intSchema()), prop("source_sha256", strSchema()), prop("parser", objReq([]string{"name", "model"}, prop("name", strSchema()), prop("model", strSchema()))), prop("coordinate_space", enumStringSchema("normalized_top_left")), prop("pages", array(objReq([]string{"index"}, prop("index", intSchema()), prop("width", map[string]any{"type": "number"}), prop("height", map[string]any{"type": "number"})))), prop("blocks", array(refSchema("PDFIRBlock"))), prop("warnings", array(strSchema())))
	return map[string]any{
		"PDFIRBlock": block, "PDFIRDocument": document,
		"PDFIRState":    objReq([]string{"status"}, prop("status", enumStringSchema("not_requested", "queued", "running", "ready", "failed", "unavailable")), prop("source_sha256", strSchema()), prop("error_message", strSchema()), prop("document", refSchema("PDFIRDocument"))),
		"PDFIREnvelope": objReq([]string{"data"}, prop("data", refSchema("PDFIRState"))),
		"PDFIRRequest":  obj(prop("force", boolSchema())),
	}
}
func pdfIROpenAPIPaths() map[string]any {
	operation := func(summary string) map[string]any {
		return map[string]any{"summary": summary, "responses": map[string]any{"200": response(200, "Current PDF reading structure state", refSchema("PDFIREnvelope")), "400": response(400, "Invalid request", refSchema("ErrorResponse")), "404": response(404, "Document missing or forbidden", refSchema("ErrorResponse")), "500": response(500, "PDF IR storage unavailable", refSchema("ErrorResponse"))}}
	}
	get := operation("Read PDF structure without triggering parsing or retrieval")
	post := operation("Parse the original PDF using the selected MinerU service independently of retrieval")
	post["requestBody"] = map[string]any{"required": false, "content": map[string]any{"application/json": map[string]any{"schema": refSchema("PDFIRRequest")}}}
	return map[string]any{"/datasets/{dataset}/documents/{document}/pdf-ir": map[string]any{"get": get, "post": post}}
}

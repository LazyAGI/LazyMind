package main

func learningOpenAPISchemas() map[string]any {
	capabilityRef := objReq([]string{"key", "enabled"}, prop("key", strSchema()), prop("version", intSchema()), prop("enabled", boolSchema()), prop("display_order", intSchema()), prop("settings", obj()))
	return map[string]any{
		"LearningCapabilityRef":           capabilityRef,
		"LearningCapabilityConfigRequest": objReq([]string{"capabilities"}, prop("profile_key", strSchema()), prop("capabilities", array(refSchema("LearningCapabilityRef")))),
		"LearningContentResolveRequest":   objReq([]string{"capability_key", "text"}, prop("capability_key", strSchema()), prop("text", strSchema()), prop("context", strSchema()), prop("language", strSchema()), prop("subject_kind", strSchema()), prop("dataset_id", strSchema()), prop("document_id", strSchema()), prop("document_revision", strSchema()), prop("target_language", strSchema()), prop("book_ids", array(strSchema()))),
		"LearningBookRequest":             objReq([]string{"name", "capability_key", "question_types"}, prop("name", strSchema()), prop("description", strSchema()), prop("capability_key", strSchema()), prop("question_types", array(strSchema())), prop("generation_policy", obj())),
		"LearningReviewSessionRequest":    objReq([]string{"book_id"}, prop("book_id", strSchema()), prop("locale", strSchema()), prop("limit", intSchema())),
		"LearningReviewAnswerRequest":     objReq([]string{"question_id", "idempotency_key"}, prop("question_id", strSchema()), prop("response", strSchema()), prop("rating", enumStringSchema("again", "hard", "good", "easy")), prop("idempotency_key", strSchema())),
		"LearningPresetRequest":           objReq([]string{"scope_type", "capability_key", "key", "value"}, prop("scope_type", enumStringSchema("user_global", "knowledge_base", "document")), prop("scope_id", strSchema()), prop("document_revision", strSchema()), prop("capability_key", strSchema()), prop("key", strSchema()), prop("value", obj()), prop("schema_version", intSchema()), prop("priority", intSchema()), prop("origin", strSchema())),
		"LearningProfileRequest":          objReq([]string{"name", "capabilities"}, prop("name", strSchema()), prop("description", strSchema()), prop("capabilities", array(refSchema("LearningCapabilityRef")))),
		"LearningPreanalysisRequest":      objReq([]string{"dataset_id", "document_id", "capability_keys"}, prop("dataset_id", strSchema()), prop("document_id", strSchema()), prop("document_revision", strSchema()), prop("capability_keys", array(strSchema())), prop("analysis_direction", strSchema()), prop("items", array(obj()))),
		"LearningDictionaryImportRequest": objReq([]string{"entries"}, prop("entries", array(obj()))),
		"LearningEnvelope":                objReq([]string{"data"}, prop("data", map[string]any{"nullable": true})),
	}
}

func learningOperation(summary string, params []map[string]any, body map[string]any) map[string]any {
	op := map[string]any{"summary": summary, "responses": map[string]any{"200": response(200, "Learning operation succeeded", refSchema("LearningEnvelope")), "400": response(400, "Invalid learning capability, schema, provider, or request", refSchema("ErrorResponse")), "401": response(401, "Gateway user identity is missing", refSchema("ErrorResponse")), "404": response(404, "Learning resource was not found", refSchema("ErrorResponse")), "409": response(409, "Learning resource state conflict", refSchema("ErrorResponse")), "503": response(503, "Desktop local backend is required", refSchema("ErrorResponse"))}}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if body != nil {
		op["requestBody"] = jsonBody(body, true)
	}
	return op
}
func lp(name string) []map[string]any {
	return []map[string]any{param("path", name, true, strSchema())}
}
func learningOpenAPIPaths() map[string]any {
	return map[string]any{
		"/learning/catalog":                                    map[string]any{"get": learningOperation("List registered learning capabilities, providers, schemas, question types, and profiles", nil, nil)},
		"/learning/profiles":                                   map[string]any{"get": learningOperation("List built-in and custom capability profiles", nil, nil), "post": learningOperation("Create a custom capability profile", nil, refSchema("LearningProfileRequest"))},
		"/learning/datasets/{dataset_id}/capabilities":         map[string]any{"get": learningOperation("Get knowledge-base capability snapshot", lp("dataset_id"), nil), "put": learningOperation("Replace knowledge-base capability snapshot", lp("dataset_id"), refSchema("LearningCapabilityConfigRequest"))},
		"/learning/presets":                                    map[string]any{"get": learningOperation("List scoped learning KV presets", nil, nil), "put": learningOperation("Create or update a scoped learning KV preset", nil, refSchema("LearningPresetRequest"))},
		"/learning/presets/{preset_id}":                        map[string]any{"patch": learningOperation("Update a learning preset", lp("preset_id"), obj()), "delete": learningOperation("Mark a learning preset stale", lp("preset_id"), nil)},
		"/learning/content:resolve":                            map[string]any{"post": learningOperation("Resolve structured learning content through the registered provider pipeline", nil, refSchema("LearningContentResolveRequest"))},
		"/learning/books":                                      map[string]any{"get": learningOperation("List learning collections", nil, nil), "post": learningOperation("Create a capability-isolated learning collection", nil, refSchema("LearningBookRequest"))},
		"/learning/books/{book_id}":                            map[string]any{"patch": learningOperation("Update a learning collection without changing capability", lp("book_id"), refSchema("LearningBookRequest")), "delete": learningOperation("Archive a learning collection while preserving review history", lp("book_id"), nil)},
		"/learning/dictionaries:import":                        map[string]any{"post": learningOperation("Import traceable dictionary entries", nil, refSchema("LearningDictionaryImportRequest"))},
		"/learning/review/sessions":                            map[string]any{"post": learningOperation("Create an immutable review question snapshot", nil, refSchema("LearningReviewSessionRequest"))},
		"/learning/review/sessions/{session_id}":               map[string]any{"get": learningOperation("Get a review session snapshot", lp("session_id"), nil)},
		"/learning/review/sessions/{session_id}/answers":       map[string]any{"post": learningOperation("Grade an answer and advance FSRS", lp("session_id"), refSchema("LearningReviewAnswerRequest"))},
		"/learning/preanalysis/tasks":                          map[string]any{"post": learningOperation("Create a document preanalysis task", nil, refSchema("LearningPreanalysisRequest"))},
		"/learning/preanalysis/tasks/{task_id}":                map[string]any{"get": learningOperation("Get preanalysis progress and partial results", lp("task_id"), nil)},
		"/learning/preanalysis/tasks/{task_id}:run":            map[string]any{"post": learningOperation("Run or retry a preanalysis task", lp("task_id"), nil)},
		"/learning/preanalysis/tasks/{task_id}:cancel":         map[string]any{"post": learningOperation("Cancel a queued or running preanalysis task", lp("task_id"), nil)},
		"/learning/preanalysis/tasks/{task_id}/drafts":         map[string]any{"get": learningOperation("Preview document preanalysis drafts", lp("task_id"), nil)},
		"/learning/preanalysis/tasks/{task_id}/drafts:publish": map[string]any{"post": learningOperation("Publish selected preanalysis drafts", lp("task_id"), obj())},
	}
}

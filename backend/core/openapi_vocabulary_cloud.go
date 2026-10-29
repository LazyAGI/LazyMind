package main

func vocabularyCloudSchemas() map[string]any {
	provider := enumStringSchema("eudic", "maimemo")
	token := map[string]any{"type": "string", "writeOnly": true, "description": "Personal provider token. Omit or leave blank to retain the stored token; never returned in responses."}
	schemas := map[string]any{
		"VocabularyCloudConnection": objReq([]string{"provider", "configured", "enabled", "target_id", "target_name", "add_to_study", "pending", "failed", "succeeded", "last_error"},
			prop("provider", provider), prop("configured", boolSchema()), prop("enabled", boolSchema()), prop("target_id", strSchema()), prop("target_name", strSchema()), prop("add_to_study", boolSchema()), prop("pending", int64Schema()), prop("failed", int64Schema()), prop("succeeded", int64Schema()), prop("last_error", strSchema())),
		"VocabularyCloudWordbook":          objReq([]string{"id", "name"}, prop("id", strSchema()), prop("name", strSchema())),
		"VocabularyCloudOperation":         objReq([]string{"id", "provider", "term", "status", "last_error", "updated_at"}, prop("id", strSchema()), prop("provider", provider), prop("term", strSchema()), prop("status", enumStringSchema("pending", "failed", "succeeded", "canceled")), prop("last_error", strSchema()), prop("updated_at", map[string]any{"type": "string", "format": "date-time"})),
		"VocabularyCloudConnectionRequest": obj(prop("token", token), prop("enabled", boolSchema()), prop("target_id", strSchema()), prop("target_name", strSchema()), prop("add_to_study", boolSchema())),
		"VocabularyCloudTokenRequest":      obj(prop("token", token)),
		"VocabularyCloudWordbookRequest":   objReq([]string{"name"}, prop("token", token), prop("name", map[string]any{"type": "string", "minLength": 1, "maxLength": 255})),
	}
	for name, data := range map[string]map[string]any{
		"Connections": objReq([]string{"items"}, prop("items", array(refSchema("VocabularyCloudConnection")))),
		"Connection":  refSchema("VocabularyCloudConnection"),
		"Wordbooks":   objReq([]string{"items"}, prop("items", array(refSchema("VocabularyCloudWordbook")))),
		"Wordbook":    refSchema("VocabularyCloudWordbook"),
		"Operations":  objReq([]string{"items"}, prop("items", array(refSchema("VocabularyCloudOperation")))),
		"Disconnect":  objReq([]string{"deleted"}, prop("deleted", boolSchema())),
		"Sync":        objReq([]string{"queued"}, prop("queued", intSchema())),
	} {
		schemas["VocabularyCloud"+name+"Response"] = objReq([]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()), prop("data", data))
	}
	return schemas
}

func vocabularyCloudPaths() map[string]any {
	operation := func(summary, result, request string, withProvider bool) map[string]any {
		var parameters []map[string]any
		if withProvider {
			parameters = []map[string]any{param("path", "provider", true, enumStringSchema("eudic", "maimemo"))}
		}
		var body map[string]any
		if request != "" {
			body = jsonBody(refSchema("VocabularyCloud"+request+"Request"), true)
		}
		value := op(summary, parameters, body, response(200, summary, refSchema("VocabularyCloud"+result+"Response")))
		value["tags"] = []string{"VocabularyCloud"}
		responses := value["responses"].(map[string]any)
		for code, description := range map[string]string{"400": "Invalid vocabulary sync configuration or word", "401": "Missing identity or invalid provider token", "403": "Permission denied", "409": "Connection disabled or missing, or remote study quota unavailable", "429": "Provider rate limit reached", "500": "Vocabulary sync state could not be saved", "502": "Remote vocabulary service failed", "503": "Vocabulary credentials unavailable on this device"} {
			responses[code] = map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": refSchema("ErrorResponse")}}}
		}
		return value
	}
	return map[string]any{
		"/vocabulary/cloud/connections":                      map[string]any{"get": operation("List the current user's vocabulary cloud connections and sync counts", "Connections", "", false)},
		"/vocabulary/cloud/connections/{provider}":           map[string]any{"put": operation("Save vocabulary cloud credentials and target settings", "Connection", "Connection", true), "delete": operation("Disconnect vocabulary cloud sync and cancel queued operations", "Disconnect", "", true)},
		"/vocabulary/cloud/connections/{provider}/test":      map[string]any{"post": operation("Test supplied or stored credentials and list wordbooks", "Wordbooks", "Token", true)},
		"/vocabulary/cloud/connections/{provider}/wordbooks": map[string]any{"post": operation("Create a remote vocabulary wordbook", "Wordbook", "Wordbook", true)},
		"/vocabulary/cloud/connections/{provider}/sync":      map[string]any{"post": operation("Queue historical local English words and retry unsuccessful sync operations", "Sync", "", true)},
		"/vocabulary/cloud/operations":                       map[string]any{"get": operation("List up to 50 recent vocabulary cloud sync operations for the current user", "Operations", "", false)},
	}
}

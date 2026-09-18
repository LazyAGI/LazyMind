package main

import "strconv"

func cloudAccountSchemas() map[string]any {
	mode := enumStringSchema("trusted_device", "temporary")
	schemas := map[string]any{
		"CloudSessionStatus": objReq([]string{"state", "configured", "reachability"},
			prop("state", enumStringSchema("signed_out", "authorizing", "exchanging", "restoring", "signed_in", "refreshing", "reauth_required", "offline")),
			prop("configured", boolSchema()), prop("reachability", enumStringSchema("unknown", "checking", "reachable", "unreachable")),
			prop("access_expires_at", dateTimeSchema()), prop("account_id", strSchema()), prop("username", strSchema()), prop("email_masked", strSchema()), prop("registration_url", strSchema())),
		"CloudLoginStart": objReq([]string{"authorization_url", "expires_in_seconds"}, prop("authorization_url", strSchema()), prop("expires_in_seconds", intSchema())),
		"CloudTokenPlanSnapshot": objReq([]string{"status"}, prop("status", enumStringSchema("inactive", "active")),
			prop("model_quotas", array(refSchema("CloudTokenPlanQuota"))), prop("usage", array(refSchema("CloudTokenPlanUsage")))),
		"CloudTokenPlanQuota": objReq([]string{"public_model_key", "capability", "meter_unit", "periodic_quota"},
			prop("public_model_key", strSchema()), prop("capability", strSchema()), prop("meter_unit", strSchema()), prop("periodic_quota", int64Schema())),
		"CloudTokenPlanUsage": objReq([]string{"public_model_key", "meter_unit", "periodic_quota", "used_amount", "remaining_amount", "missing_usage_count"},
			prop("public_model_key", strSchema()), prop("meter_unit", strSchema()), prop("periodic_quota", int64Schema()), prop("used_amount", int64Schema()), prop("remaining_amount", int64Schema()), prop("missing_usage_count", int64Schema())),
		"CredentialBackupStatus": objReq([]string{"available", "enabled", "backed_up", "pending", "failed"},
			prop("available", boolSchema()), prop("reason_code", strSchema()), prop("enabled", boolSchema()), prop("backed_up", int64Schema()), prop("pending", int64Schema()), prop("failed", int64Schema()), prop("last_succeeded_at", dateTimeSchema())),
		"CredentialRestoreRecord": objReq([]string{"record_id", "revision", "updated_at"}, prop("record_id", strSchema()), prop("revision", int64Schema()), prop("updated_at", dateTimeSchema())),
		"CredentialRestoreDiscovery": objReq([]string{"available", "requires_explicit_action", "records"},
			prop("available", boolSchema()), prop("reason_code", strSchema()), prop("requires_explicit_action", boolSchema()), prop("records", array(refSchema("CredentialRestoreRecord"))), prop("active_operation", refSchema("CredentialRestoreOperation"))),
		"CredentialRestoreOperation": objReq([]string{"operation_id", "status", "mode", "total_records", "completed_records", "expires_at"},
			prop("operation_id", strSchema()), prop("status", enumStringSchema("pending", "running", "succeeded", "failed", "expired", "canceled")), prop("mode", mode),
			prop("total_records", intSchema()), prop("completed_records", intSchema()), prop("expires_at", dateTimeSchema()), prop("temporary_expires_at", dateTimeSchema()), prop("failure_code", strSchema())),
		"CredentialRestoreRequest": objReq([]string{"mode", "records"}, prop("mode", mode),
			prop("records", array(refSchema("CredentialRestoreSelection")))),
		"CredentialRestoreSelection": objReq([]string{"record_id", "revision", "resolution"},
			prop("record_id", strSchema()), prop("revision", int64Schema()), prop("resolution", enumStringSchema("fail", "replace_local", "save_copy"))),
	}
	for _, name := range []string{"CloudSessionStatus", "CloudLoginStart", "CloudTokenPlanSnapshot", "CredentialBackupStatus", "CredentialRestoreDiscovery", "CredentialRestoreOperation"} {
		schemas[name+"Response"] = objReq([]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()), prop("data", refSchema(name)))
	}
	return schemas
}

func cloudAccountPaths() map[string]any {
	operation := func(summary string, params []map[string]any, body map[string]any, schema string, errors ...int) map[string]any {
		value := op(summary, params, body, response(200, summary, refSchema(schema+"Response")))
		if schema == "" {
			value["responses"] = map[string]any{"204": map[string]any{"description": "No Content"}}
		}
		for _, status := range errors {
			value["responses"].(map[string]any)[strconv.Itoa(status)] = response(status, "Cloud request failed", refSchema("ErrorResponse"))
		}
		return value
	}
	restoreErrors := []int{400, 401, 403, 404, 409, 410, 429, 502, 503}
	operationID := queryParams(param("path", "operation_id", true, strSchema()))
	return map[string]any{
		"/cloud/session":                   map[string]any{"get": operation("Get Cloud session status", nil, nil, "CloudSessionStatus")},
		"/cloud/login":                     map[string]any{"post": operation("Begin Cloud login", nil, nil, "CloudLoginStart", 502, 503)},
		"/cloud/logout":                    map[string]any{"post": operation("Log out of Cloud", nil, nil, "CloudSessionStatus", 503)},
		"/cloud/token-plan":                map[string]any{"get": operation("Get Cloud token plan and usage", nil, nil, "CloudTokenPlanSnapshot", 401, 403, 429, 502, 503)},
		"/credential-vault/backup":         map[string]any{"get": operation("Get credential backup status", nil, nil, "CredentialBackupStatus", 503)},
		"/credential-vault/backup:enable":  map[string]any{"post": operation("Enable credential backup", nil, nil, "CredentialBackupStatus", 503)},
		"/credential-vault/backup:disable": map[string]any{"post": operation("Disable credential backup", nil, nil, "CredentialBackupStatus", 503)},
		"/credential-vault/restores": map[string]any{
			"get":  operation("Discover credential backups", nil, nil, "CredentialRestoreDiscovery", restoreErrors...),
			"post": operation("Start credential restore", nil, jsonBody(refSchema("CredentialRestoreRequest"), true), "CredentialRestoreOperation", restoreErrors...),
		},
		"/credential-vault/restores/{operation_id}": map[string]any{
			"get":    operation("Get credential restore progress", operationID, nil, "CredentialRestoreOperation", restoreErrors...),
			"delete": operation("Cancel credential restore", operationID, nil, "", restoreErrors...),
		},
		"/credential-vault/restores:clear-temporary": map[string]any{"post": operation("Clear temporary credentials", nil, nil, "", restoreErrors...)},
	}
}

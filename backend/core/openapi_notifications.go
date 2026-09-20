package main

import "strings"

func notificationSchemas() map[string]any {
	strict := func(value map[string]any) map[string]any { value["additionalProperties"] = false; return value }
	events := map[string]any{}
	channels := map[string]any{}
	for _, name := range []string{"succeeded", "failed", "waiting"} {
		events[name] = strict(objReq([]string{"enabled", "content"}, prop("enabled", boolSchema()), prop("content", enumStringSchema("summary", "full"))))
	}
	for _, name := range []string{"desktop", "wechat", "feishu", "wecom"} {
		channels[name] = strict(objReq([]string{"enabled"}, prop("enabled", boolSchema()), prop("account_id", map[string]any{"type": "string", "maxLength": 256}), prop("recipient_id", map[string]any{"type": "string", "maxLength": 256})))
	}
	config := strict(objReq([]string{"events", "channels"}, prop("events", map[string]any{"type": "object", "required": []string{"succeeded", "failed", "waiting"}, "additionalProperties": false, "properties": events}), prop("channels", map[string]any{"type": "object", "minProperties": 1, "maxProperties": 4, "additionalProperties": false, "properties": channels})))
	batchTask := strict(objReq([]string{"client_key", "name", "cron_expr", "prompt_template"},
		prop("client_key", strSchema()), prop("name", strSchema()), prop("remark", strSchema()),
		prop("cron_expr", strSchema()), prop("timezone", strSchema()), prop("prompt_template", strSchema()),
		prop("kb_ids", array(strSchema())), prop("file_ids", array(strSchema())), prop("dependencies", array(obj())),
		prop("notification", refSchema("ScheduleNotificationUpdate"))))
	return map[string]any{
		"NotificationConfig": config,
		"NotificationError": objReq([]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()),
			prop("data", obj(prop("detail", objReq([]string{"reason", "request_id"}, prop("reason", strSchema()), prop("request_id", strSchema()), prop("running_task_ids", array(strSchema()))))))),
		"NotificationPreferences":      objReq([]string{"enabled", "revision", "defaults"}, prop("enabled", boolSchema()), prop("revision", int64Schema()), prop("defaults", refSchema("NotificationConfig"))),
		"NotificationPreferencesPatch": strict(objReq([]string{"revision"}, prop("revision", int64Schema()), prop("enabled", boolSchema()), prop("defaults", refSchema("NotificationConfig")), prop("confirm_running_task_ids", array(strSchema())))),
		"ScheduleNotificationUpdate":   strict(objReq([]string{"revision"}, prop("revision", int64Schema()), prop("config", refSchema("NotificationConfig")), prop("clear", boolSchema()))),
		"ScheduleNotificationReset":    strict(objReq([]string{"revision"}, prop("revision", int64Schema()))),
		"ScheduleNotificationView":     objReq([]string{"configured", "revision", "config"}, prop("configured", boolSchema()), prop("revision", int64Schema()), prop("config", nullableSchema(refSchema("NotificationConfig"))), prop("availability", obj())),
		"AutomationGroupBatchCreateRequest": strict(objReq([]string{"group", "tasks"},
			prop("group", strict(objReq([]string{"name", "timezone"}, prop("name", strSchema()), prop("remark", strSchema()), prop("timezone", strSchema())))),
			prop("tasks", array(batchTask)))),
		"AutomationGroupBatchCreateResponse": objReq([]string{"group_id", "schedule_ids"}, prop("group_id", strSchema()), prop("schedule_ids", map[string]any{"type": "object", "additionalProperties": strSchema()})),
		"TaskNotification": objReq([]string{"notification_id", "task_id", "schedule_id", "event_id", "event", "channel", "title", "body", "status", "created_at"},
			prop("notification_id", strSchema()), prop("user_id", strSchema()), prop("task_id", strSchema()), prop("schedule_id", strSchema()), prop("event_id", strSchema()), prop("event", enumStringSchema("succeeded", "failed", "waiting")), prop("channel", enumStringSchema("desktop", "wechat", "feishu", "wecom")), prop("account_id", strSchema()), prop("recipient_id", strSchema()), prop("config_revision", int64Schema()), prop("title", strSchema()), prop("body", strSchema()), prop("content", enumStringSchema("summary", "full")), prop("status", enumStringSchema("pending", "queued", "sending", "sent", "failed", "unknown", "skipped", "unavailable")), prop("reason", strSchema()), prop("gateway_id", strSchema()), prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()), prop("app_name", strSchema()), prop("execution_id", strSchema()), prop("navigation", obj(prop("type", strSchema()), prop("task_id", strSchema()), prop("schedule_id", strSchema())))),
		"TaskNotifications":          obj(prop("snapshot", obj(prop("revision", int64Schema()), prop("config", nullableSchema(refSchema("NotificationConfig"))))), prop("items", array(refSchema("TaskNotification")))),
		"DesktopNotifications":       obj(prop("items", array(refSchema("TaskNotification"))), prop("next_cursor", strSchema()), prop("device_id", strSchema())),
		"DesktopNotificationAck":     strict(objReq([]string{"device_id", "status"}, prop("device_id", enumStringSchema("local", "browser")), prop("status", enumStringSchema("delivered", "permission_denied")))),
		"DesktopNotificationReceipt": obj(prop("notification_id", strSchema()), prop("device_id", strSchema()), prop("status", strSchema()), prop("reason", strSchema()), prop("created_at", dateTimeSchema())),
		"NotificationClaim":          strict(objReq([]string{"outbox_id"}, prop("outbox_id", strSchema()), prop("retry", boolSchema()))),
		"NotificationClaimResult":    obj(prop("granted", boolSchema())),
		"NotificationReferences":     obj(prop("items", array(obj(prop("id", strSchema()), prop("kind", enumStringSchema("defaults", "schedule", "run")), prop("name", strSchema()), prop("enabled", boolSchema())))), prop("total", intSchema()), prop("next_cursor", strSchema())),
	}
}

func notificationPaths() map[string]any {
	paths := map[string]any{}
	for _, entry := range []struct{ Path, Method, Input, Output, Description string }{
		{"/user/notification-preferences", "get", "", "NotificationPreferences", "Read scheduled-task notification defaults and global gate"},
		{"/user/notification-preferences", "patch", "NotificationPreferencesPatch", "NotificationPreferences", "Compare revision; closing requires the exact current affected run IDs"},
		{"/schedules/{schedule_id}/notifications", "get", "", "ScheduleNotificationView", "Read configuration and current channel availability"},
		{"/schedules/{schedule_id}/notifications", "put", "ScheduleNotificationUpdate", "ScheduleNotificationView", "Update next-run configuration with an independent revision"},
		{"/schedules/{schedule_id}/notifications:reset", "post", "ScheduleNotificationReset", "ScheduleNotificationView", "Copy current defaults into this schedule"},
		{"/task-center/tasks/{task_id}/notifications", "get", "", "TaskNotifications", "Read immutable run configuration and delivery history"},
		{"/task-center/desktop-notifications", "get", "", "DesktopNotifications", "System notifications: browser in all deployments; local native client in Local/Desktop only"},
		{"/task-center/desktop-notifications/{notification_id}:ack", "post", "DesktopNotificationAck", "DesktopNotificationReceipt", "Idempotent system-notification receipt; delivered means submitted, not seen by the user"},
		{"/notification-account-references/{account_id}", "get", "", "NotificationReferences", "Read owned defaults, schedules and active runs referencing an account"},
		{"/task-center/notification-events/{notification_id}:claim", "post", "NotificationClaim", "NotificationClaimResult", "Internal service token required; atomically grant one segment against the global gate"},
	} {
		params := []map[string]any{}
		for _, name := range []string{"schedule_id", "task_id", "notification_id", "account_id"} {
			if strings.Contains(entry.Path, "{"+name+"}") {
				params = append(params, param("path", name, true, strSchema()))
			}
		}
		if entry.Output == "DesktopNotifications" || entry.Output == "NotificationReferences" {
			params = append(params, param("query", "cursor", false, strSchema()), param("query", "limit", false, map[string]any{"type": "integer", "default": 20, "minimum": 1, "maximum": 100}))
		}
		if entry.Output == "DesktopNotifications" {
			params = append(params, param("query", "device_id", false, enumStringSchema("local", "browser")))
		}
		if entry.Input == "NotificationClaim" {
			params = append(params, param("header", "X-LazyMind-Internal-Token", true, strSchema()))
		}
		var body map[string]any
		if entry.Input != "" {
			body = jsonBody(refSchema(entry.Input), true)
		}
		operation := op(entry.Description, params, body, response(200, "Success", obj(prop("code", intSchema()), prop("data", refSchema(entry.Output)))))
		operation["tags"] = []string{"TaskNotifications"}
		operation["description"] = entry.Description + ". Configuration bodies are limited to 16 KiB; errors include stable data.detail.reason and request_id."
		responses := operation["responses"].(map[string]any)
		for _, code := range []string{"401", "403", "404", "409", "413", "422", "500", "503"} {
			responses[code] = map[string]any{"description": "Notification request failed", "content": map[string]any{"application/json": map[string]any{"schema": refSchema("NotificationError")}}}
		}
		if paths[entry.Path] == nil {
			paths[entry.Path] = map[string]any{}
		}
		paths[entry.Path].(map[string]any)[entry.Method] = operation
	}
	scheduleBody := obj(prop("name", strSchema()), prop("remark", strSchema()), prop("prompt_template", strSchema()), prop("cron_expr", strSchema()), prop("timezone", strSchema()), prop("kb_ids", array(strSchema())), prop("file_ids", array(strSchema())), prop("group_id", nullableSchema(strSchema())), prop("dependencies", array(obj())), prop("notification", refSchema("ScheduleNotificationUpdate")))
	for _, entry := range []struct{ path, method string }{{"/schedules", "post"}, {"/schedules/{schedule_id}", "put"}} {
		params := []map[string]any{}
		if entry.method == "put" {
			params = append(params, param("path", "schedule_id", true, strSchema()))
		}
		operation := op("Save schedule and optional notification draft atomically", params, jsonBody(scheduleBody, true), response(200, "Schedule", obj(prop("id", strSchema()), prop("name", strSchema()))))
		operation["description"] = "notification is optional. Omission retains legacy defaults on creation and preserves the rule on update. Send config or clear:true, not both. Updates compare notification.revision; conflicts roll back all task edits. Creation ignores the supplied revision. Runs and history are unchanged."
		operation["tags"] = []string{"TaskNotifications"}
		paths[entry.path] = map[string]any{entry.method: operation}
	}
	batch := op("Create a task group and save each task's optional notification draft atomically", nil,
		jsonBody(refSchema("AutomationGroupBatchCreateRequest"), true),
		response(200, "Created task group", refSchema("AutomationGroupBatchCreateResponse")))
	batch["description"] = "Each tasks[].notification belongs only to that concrete schedule. Omission keeps the current default snapshot. Any task, dependency, or notification failure rolls back the entire group."
	batch["tags"] = []string{"TaskNotifications"}
	paths["/automation-groups:batch-create"] = map[string]any{"post": batch}

	return paths
}

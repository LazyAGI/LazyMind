// Stable errors discovered by the Core error-constructor audit.

package common

import "net/http"

func init() {
	registerAdditionalErrorPattern("%w; fallback parser chunks failed", "Primary and fallback document parsing failed", http.StatusInternalServerError, 2001601)
	registerAdditionalError("acl_db_dsn is empty", http.StatusInternalServerError, 2001602)
	registerAdditionalError("active plugin session already exists for conversation", http.StatusConflict, 2001603)
	registerAdditionalErrorPattern("active thread reservation no longer owns user %s", "Active thread reservation no longer owns the user", http.StatusInternalServerError, 2001604)
	registerAdditionalError("another active or waiting session exists for this conversation", http.StatusConflict, 2001605)
	registerAdditionalErrorAlias("another non-dismissed workflow session exists for this conversation", "another active or waiting session exists for this conversation", http.StatusConflict, 2001605)
	registerAdditionalError("artifact caption is too long", http.StatusBadRequest, 2001606)
	registerAdditionalError("artifact event is required", http.StatusBadRequest, 2001607)
	registerAdditionalError("artifact id already exists", http.StatusConflict, 2001608)
	registerAdditionalError("artifact replacement scope mismatch", http.StatusBadRequest, 2001609)
	registerAdditionalError("artifact value must be an object", http.StatusBadRequest, 2001610)
	registerAdditionalError("async job_type is required", http.StatusBadRequest, 2001611)
	registerAdditionalError("at least one field required", http.StatusBadRequest, 2001612)
	registerAdditionalError("at least one transition target is required", http.StatusBadRequest, 2001613)
	registerAdditionalError("at most 3 conversation mentions are allowed", http.StatusBadRequest, 2001614)
	registerAdditionalError("bad request", http.StatusBadRequest, 2001615)
	registerAdditionalError("base task/document set size mismatch", http.StatusBadRequest, 2001616)
	registerAdditionalError("binary blob has no storage key", http.StatusInternalServerError, 2001617)
	registerAdditionalErrorPattern("binary blob storage key missing for %s", "Binary blob storage key is missing", http.StatusInternalServerError, 2001618)
	registerAdditionalError("binary content is not available", http.StatusBadRequest, 2001619)
	registerAdditionalErrorAlias("basic chat does not support workflow mentions", "Conflict", http.StatusConflict, 2000107)
	registerAdditionalError("build invalid rows csv url failed", http.StatusInternalServerError, 2001620)
	registerAdditionalErrorPattern("builtin skill %s missing skill.md", "Built-in skill is missing skill.md", http.StatusInternalServerError, 2001621)
	registerAdditionalError("builtin skills root not found", http.StatusNotFound, 2001622)
	registerAdditionalError("cannot create directory over file", http.StatusBadRequest, 2001623)
	registerAdditionalError("cannot delete draft base revision", http.StatusBadRequest, 2001624)
	registerAdditionalError("cannot delete head revision", http.StatusBadRequest, 2001625)
	registerAdditionalError("cannot mention the current conversation", http.StatusBadRequest, 2001626)
	registerAdditionalError("cannot merge hunks", http.StatusBadRequest, 2001627)
	registerAdditionalError("cannot move directory into itself", http.StatusBadRequest, 2001628)
	registerAdditionalError("cannot move folder into its descendant", http.StatusBadRequest, 2001629)
	registerAdditionalError("cannot replace source while draft overlay exists", http.StatusBadRequest, 2001630)
	registerAdditionalError("cannot rollback while draft overlay exists", http.StatusBadRequest, 2001631)
	registerAdditionalErrorPattern("cannot set intent field %q", "The specified intent field cannot be set", http.StatusBadRequest, 2001632)
	registerAdditionalError("cannot update skill metadata while draft overlay exists", http.StatusBadRequest, 2001633)
	registerAdditionalError("case_id too long", http.StatusBadRequest, 2001634)
	registerAdditionalError("catalog yaml path is required", http.StatusBadRequest, 2001635)
	registerAdditionalError("child name required", http.StatusBadRequest, 2001636)
	registerAdditionalError("child path conflicts with skill.md", http.StatusConflict, 2001637)
	registerAdditionalError("chmod utf-8 temp file", http.StatusInternalServerError, 2001638)
	registerAdditionalError("close paddleocr multipart body", http.StatusInternalServerError, 2001639)
	registerAdditionalError("close utf-8 temp file", http.StatusInternalServerError, 2001640)
	registerAdditionalError("compiled graph hash does not match revision metadata", http.StatusInternalServerError, 2001641)
	registerAdditionalError("content_hash is only supported for parse tasks", http.StatusBadRequest, 2001642)
	registerAdditionalError("conversation mention is not readable", http.StatusForbidden, 2001643)
	registerAdditionalErrorPattern("convert attachment %q", "Failed to convert the attachment", http.StatusInternalServerError, 2001644)
	registerAdditionalError("convert response missing pdf_path", http.StatusInternalServerError, 2001645)
	registerAdditionalError("converted pdf not found", http.StatusNotFound, 2001646)
	registerAdditionalError("converted pdf not found after libreoffice run", http.StatusNotFound, 2001647)
	registerAdditionalError("create dir failed", http.StatusInternalServerError, 2001648)
	registerAdditionalError("create file failed", http.StatusInternalServerError, 2001649)
	registerAdditionalError("create final dir failed", http.StatusInternalServerError, 2001650)
	registerAdditionalError("create merged file failed", http.StatusInternalServerError, 2001651)
	registerAdditionalError("create reusable file dir failed", http.StatusInternalServerError, 2001652)
	registerAdditionalError("create reusable upload record failed", http.StatusInternalServerError, 2001653)
	registerAdditionalError("create subagent task", http.StatusInternalServerError, 2001654)
	registerAdditionalError("create task failed", http.StatusInternalServerError, 2001655)
	registerAdditionalError("create upload dir failed", http.StatusInternalServerError, 2001656)
	registerAdditionalError("create upload session failed", http.StatusInternalServerError, 2001657)
	registerAdditionalError("create uploaded task failed", http.StatusInternalServerError, 2001658)
	registerAdditionalError("create utf-8 temp file", http.StatusInternalServerError, 2001659)
	registerAdditionalError("cron expression must have 5 fields (minute hour dom month dow)", http.StatusBadRequest, 2001660)
	registerAdditionalError("cron expression produces no future times within 1 year", http.StatusInternalServerError, 2001661)
	registerAdditionalError("database_name required", http.StatusBadRequest, 2001662)
	registerAdditionalError("dataset context is required", http.StatusBadRequest, 2001663)
	registerAdditionalErrorAlias("dataset is required", "Dataset is required", http.StatusBadRequest, 2000206)
	registerAdditionalError("dataset kb mapping not found", http.StatusNotFound, 2001665)
	registerAdditionalError("dataset name is required", http.StatusBadRequest, 2001666)
	registerAdditionalError("dataset_ids too long", http.StatusBadRequest, 2001667)
	registerAdditionalError("db is not configured", http.StatusInternalServerError, 2001668)
	registerAdditionalError("db_type must be mysql or postgresql", http.StatusBadRequest, 2001669)
	registerAdditionalError("decode compiled graph", http.StatusInternalServerError, 2001670)
	registerAdditionalError("decode import job payload", http.StatusInternalServerError, 2001671)
	registerAdditionalError("decode import temp rows", http.StatusInternalServerError, 2001672)
	registerAdditionalError("decode mcp rpc response", http.StatusBadGateway, 2001673)
	registerAdditionalError("decode payload", http.StatusInternalServerError, 2001674)
	registerAdditionalError("decode repair payload", http.StatusInternalServerError, 2001675)
	registerAdditionalError("description too long", http.StatusBadRequest, 2001676)
	registerAdditionalError("directory is not empty", http.StatusInternalServerError, 2001677)
	registerAdditionalErrorPattern("dirty database version %d", "Database migration version is dirty", http.StatusInternalServerError, 2001678)
	registerAdditionalError("document_id and document_ids cannot be set together", http.StatusBadRequest, 2001679)
	registerAdditionalError("document_id is required", http.StatusBadRequest, 2001680)
	registerAdditionalError("document_id/document_ids cannot be set when an uploaded file is used", http.StatusBadRequest, 2001681)
	registerAdditionalError("download failed", http.StatusBadGateway, 2001682)
	registerAdditionalError("download not found", http.StatusNotFound, 2001683)
	registerAdditionalError("downloader is not configured", http.StatusBadGateway, 2001684)
	registerAdditionalError("draft not found", http.StatusNotFound, 2001685)
	registerAdditionalError("draft overlay is empty", http.StatusInternalServerError, 2001686)
	registerAdditionalError("draft ref requires skill_id", http.StatusInternalServerError, 2001687)
	registerAdditionalError("draft snapshot changed", http.StatusConflict, 2001688)
	registerAdditionalErrorPattern("duplicate child path %s", "Child path is duplicated", http.StatusBadRequest, 2001689)
	registerAdditionalErrorPattern("duplicate down migration for version %d", "Down migration version is duplicated", http.StatusBadRequest, 2001690)
	registerAdditionalErrorPattern("duplicate migration version %d with different names", "Migration version has conflicting names", http.StatusBadRequest, 2001691)
	registerAdditionalError("duplicate skill path", http.StatusBadRequest, 2001692)
	registerAdditionalErrorPattern("duplicate target step %q", "Target step is duplicated", http.StatusBadRequest, 2001693)
	registerAdditionalErrorPattern("duplicate task id %q", "Task ID is duplicated", http.StatusBadRequest, 2001694)
	registerAdditionalErrorPattern("duplicate up migration for version %d", "Up migration version is duplicated", http.StatusBadRequest, 2001695)
	registerAdditionalError("empty base task/document set", http.StatusBadRequest, 2001696)
	registerAdditionalError("empty dsn", http.StatusBadRequest, 2001697)
	registerAdditionalError("expected_review_version required", http.StatusBadRequest, 2001698)
	registerAdditionalError("expression must be an object, not natural language", http.StatusBadRequest, 2001699)
	registerAdditionalError("expression must contain material, all, or any", http.StatusBadRequest, 2001700)
	registerAdditionalError("external add result cannot be matched precisely", http.StatusBadRequest, 2001701)
	registerAdditionalError("file artifact does not exist", http.StatusNotFound, 2001702)
	registerAdditionalError("file artifact filename does not match metadata", http.StatusInternalServerError, 2001703)
	registerAdditionalError("file artifact must be a regular file", http.StatusBadRequest, 2001704)
	registerAdditionalError("file artifact path escapes its conversation workspace", http.StatusForbidden, 2001705)
	registerAdditionalError("file artifact path is invalid", http.StatusBadRequest, 2001706)
	registerAdditionalError("file artifact path is outside its conversation workspace", http.StatusForbidden, 2001707)
	registerAdditionalError("file artifact value is invalid", http.StatusBadRequest, 2001708)
	registerAdditionalError("file artifact value must be an object", http.StatusBadRequest, 2001709)
	registerAdditionalError("file artifact value must contain path", http.StatusBadRequest, 2001710)
	registerAdditionalError("folder document cannot be reparsed", http.StatusBadRequest, 2001711)
	registerAdditionalError("generate endpoint returned empty content", http.StatusBadRequest, 2001712)
	registerAdditionalError("ground_truth required", http.StatusBadRequest, 2001713)
	registerAdditionalError("group_id too long", http.StatusBadRequest, 2001714)
	registerAdditionalError("hashes is required", http.StatusBadRequest, 2001715)
	registerAdditionalErrorPattern("hashes must contain at most %d items", "Too many hashes were provided", http.StatusBadRequest, 2001716)
	registerAdditionalError("head ref requires skill_id", http.StatusInternalServerError, 2001717)
	registerAdditionalError("host required", http.StatusBadRequest, 2001718)
	registerAdditionalError("host, database_name and username are required", http.StatusBadRequest, 2001719)
	registerAdditionalErrorPattern("http_status=%d", "Resource update request failed", http.StatusInternalServerError, 2001720)
	registerAdditionalErrorPattern("http_status=%d code=%d status=%q requestid=%q", "Resource update service returned an error", http.StatusInternalServerError, 2001721)
	registerAdditionalErrorPattern("http_status=%d status=%q task_id=%q", "Resource update task failed", http.StatusInternalServerError, 2001722)
	registerAdditionalError("idle processor db is nil", http.StatusInternalServerError, 2001723)
	registerAdditionalError("idle processor state store is nil", http.StatusInternalServerError, 2001724)
	registerAdditionalError("idle recorder db is nil", http.StatusInternalServerError, 2001725)
	registerAdditionalError("idle recorder state store is nil", http.StatusInternalServerError, 2001726)
	registerAdditionalError("intent value is required", http.StatusBadRequest, 2001727)
	registerAdditionalError("invalid artifact id", http.StatusBadRequest, 2001728)
	registerAdditionalError("invalid artifact metadata", http.StatusBadRequest, 2001729)
	registerAdditionalError("invalid artifact value", http.StatusBadRequest, 2001730)
	registerAdditionalError("invalid content_hash", http.StatusBadRequest, 2001731)
	registerAdditionalError("invalid cursor", http.StatusBadRequest, 2001732)
	registerAdditionalError("invalid decision", http.StatusBadRequest, 2001733)
	registerAdditionalError("invalid import mode", http.StatusBadRequest, 2001734)
	registerAdditionalError("invalid import payload", http.StatusBadRequest, 2001735)
	registerAdditionalErrorPattern("invalid intent operation %q for field %q", "Invalid operation for the specified intent field", http.StatusBadRequest, 2001736)
	registerAdditionalError("invalid json value", http.StatusBadRequest, 2001737)
	registerAdditionalError("invalid mentions", http.StatusBadRequest, 2001738)
	registerAdditionalError("invalid personal resource review request", http.StatusBadRequest, 2001739)
	registerAdditionalError("invalid port", http.StatusBadRequest, 2001740)
	registerAdditionalError("invalid preference frontmatter", http.StatusBadRequest, 2001741)
	registerAdditionalError("invalid preference frontmatter yaml", http.StatusBadRequest, 2001742)
	registerAdditionalError("invalid resource path", http.StatusBadRequest, 2001743)
	registerAdditionalError("invalid resource type", http.StatusBadRequest, 2001744)
	registerAdditionalErrorPattern("invalid script path %q", "Invalid script path", http.StatusBadRequest, 2001745)
	registerAdditionalError("invalid scripts_content", http.StatusBadRequest, 2001746)
	registerAdditionalError("invalid sha-256 hash", http.StatusBadRequest, 2001747)
	registerAdditionalError("invalid skill frontmatter", http.StatusBadRequest, 2001748)
	registerAdditionalError("invalid skill name", http.StatusBadRequest, 2001749)
	registerAdditionalError("invalid skill path", http.StatusBadRequest, 2001750)
	registerAdditionalError("invalid skill.md frontmatter", http.StatusBadRequest, 2001751)
	registerAdditionalError("invalid skill.md frontmatter field \"name\"", http.StatusBadRequest, 2001752)
	registerAdditionalError("invalid time", http.StatusBadRequest, 2001753)
	registerAdditionalError("invalid user_preference frontmatter", http.StatusBadRequest, 2001754)
	registerAdditionalError("is_deleted 必须是布尔值", http.StatusInternalServerError, 2001755)
	registerAdditionalErrorAlias("items required", "Items are required", http.StatusBadRequest, 2000218)
	registerAdditionalError("json artifact value must contain data", http.StatusBadRequest, 2001757)
	registerAdditionalError("knowledge base mention is not readable", http.StatusForbidden, 2001758)
	registerAdditionalError("knowledge base mention not found", http.StatusNotFound, 2001759)
	registerAdditionalError("lazymind_office_convert_url is not configured", http.StatusInternalServerError, 2001760)
	registerAdditionalError("legacy plugin cannot be compiled", http.StatusBadRequest, 2001761)
	registerAdditionalError("libreoffice convert failed", http.StatusInternalServerError, 2001762)
	registerAdditionalError("libreoffice convert timeout", http.StatusInternalServerError, 2001763)
	registerAdditionalError("link reusable file failed", http.StatusInternalServerError, 2001764)
	registerAdditionalErrorPattern("list chat-enabled %s connections", "Failed to list chat-enabled connections", http.StatusInternalServerError, 2001765)
	registerAdditionalError("load builtin plugin settings", http.StatusInternalServerError, 2001766)
	registerAdditionalError("load legacy plugin spec", http.StatusInternalServerError, 2001767)
	registerAdditionalError("load model configs", http.StatusInternalServerError, 2001768)
	registerAdditionalError("load plugin catalog", http.StatusInternalServerError, 2001769)
	registerAdditionalErrorPattern("load plugin revision %s", "Failed to load the plugin revision", http.StatusInternalServerError, 2001770)
	registerAdditionalError("market item id is required", http.StatusBadRequest, 2001771)
	registerAdditionalError("market source skill ownership mismatch", http.StatusForbidden, 2001772)
	registerAdditionalError("marshal async job payload", http.StatusInternalServerError, 2001773)
	registerAdditionalError("marshal bocha check body", http.StatusInternalServerError, 2001774)
	registerAdditionalError("marshal evo request", http.StatusInternalServerError, 2001775)
	registerAdditionalError("marshal mineru check body", http.StatusInternalServerError, 2001776)
	registerAdditionalErrorPattern("marshal provider %q descriptions", "Failed to serialize provider descriptions", http.StatusInternalServerError, 2001777)
	registerAdditionalError("marshal review request", http.StatusInternalServerError, 2001778)
	registerAdditionalError("marshal sciverse check body", http.StatusInternalServerError, 2001779)
	registerAdditionalError("marshal tavily check body", http.StatusInternalServerError, 2001780)
	registerAdditionalErrorPattern("mcp rpc error %d", "MCP RPC returned an error", http.StatusBadGateway, 2001781)
	registerAdditionalErrorPattern("mcp rpc returned %d", "MCP RPC request failed", http.StatusBadGateway, 2001782)
	registerAdditionalError("mcp sse endpoint event not found", http.StatusNotFound, 2001783)
	registerAdditionalErrorPattern("mcp sse returned %d", "MCP SSE request failed", http.StatusBadGateway, 2001784)
	registerAdditionalError("mention resource_id required", http.StatusBadRequest, 2001785)
	registerAdditionalError("mentioned skill is not accessible", http.StatusForbidden, 2001786)
	registerAdditionalError("mentioned skill is unpublished", http.StatusInternalServerError, 2001787)
	registerAdditionalErrorAlias("workflow mention is not accessible", "forbidden", http.StatusForbidden, 2000102)
	registerAdditionalError("merge part failed", http.StatusInternalServerError, 2001788)
	registerAdditionalErrorPattern("missing down migration for version %d", "Down migration is missing", http.StatusInternalServerError, 2001789)
	registerAdditionalErrorPattern("missing migration file for applied version %d", "Applied migration file is missing", http.StatusNotFound, 2001790)
	registerAdditionalErrorPattern("missing table %s", "Required database table is missing", http.StatusNotFound, 2001791)
	registerAdditionalError("model max_input_tokens is only supported for llm or vlm models", http.StatusBadRequest, 2001792)
	registerAdditionalError("model max_input_tokens must use a positive k or m value, for example 128k or 1m", http.StatusBadRequest, 2001793)
	registerAdditionalError("model name and type are required", http.StatusBadRequest, 2001794)
	registerAdditionalError("move file failed", http.StatusInternalServerError, 2001795)
	registerAdditionalError("no healthy router child process", http.StatusInternalServerError, 2001796)
	registerAdditionalErrorAlias("non-dismissed workflow session already exists for conversation", "another active or waiting session exists for this conversation", http.StatusConflict, 2001605)
	registerAdditionalError("no tasks submitted successfully", http.StatusInternalServerError, 2001797)
	registerAdditionalError("no uploaded parts", http.StatusInternalServerError, 2001798)
	registerAdditionalError("no valid tasks to start", http.StatusBadRequest, 2001799)
	registerAdditionalError("not a string", http.StatusBadRequest, 2001800)
	registerAdditionalError("object store is nil", http.StatusInternalServerError, 2001801)
	registerAdditionalError("order list contains hidden or unknown list_index", http.StatusBadRequest, 2001802)
	registerAdditionalError("parent path is a file", http.StatusInternalServerError, 2001803)
	registerAdditionalError("parse zip file failed", http.StatusInternalServerError, 2001804)
	registerAdditionalError("part not found", http.StatusNotFound, 2001805)
	registerAdditionalError("path already exists", http.StatusConflict, 2001806)
	registerAdditionalError("pending hunks exist", http.StatusConflict, 2001807)
	registerAdditionalErrorPattern("personal resource %s head is binary", "Personal resource head revision is binary", http.StatusInternalServerError, 2001808)
	registerAdditionalError("personal resource conflict", http.StatusConflict, 2001809)
	registerAdditionalError("personal resource draft changed before auto commit", http.StatusInternalServerError, 2001810)
	registerAdditionalError("personal resource draft not found", http.StatusNotFound, 2001811)
	registerAdditionalError("personal resource not found", http.StatusNotFound, 2001812)
	registerAdditionalError("personal resource operation unsupported", http.StatusBadRequest, 2001813)
	registerAdditionalError("personal resource review session not found", http.StatusNotFound, 2001814)
	registerAdditionalError("personal resource revision not found", http.StatusNotFound, 2001815)
	registerAdditionalError("phase1 skeleton", http.StatusInternalServerError, 2001816)
	registerAdditionalError("phase2 state_machine", http.StatusInternalServerError, 2001817)
	registerAdditionalError("invalid start_phase", http.StatusBadRequest, 2001995)
	registerAdditionalError("invalid generation resume point", http.StatusBadRequest, 2001996)
	registerAdditionalError("resume point invalid", http.StatusBadRequest, 2001997)
	registerAdditionalError("phase1 skeleton invalid", http.StatusBadGateway, 2001998)
	registerAdditionalError("phase2 workflow invalid", http.StatusBadGateway, 2001999)
	registerAdditionalError("workflow_yaml is empty", http.StatusBadGateway, 2002000)
	registerAdditionalError("workflow_yaml invalid", http.StatusBadGateway, 2002001)
	registerAdditionalError("workflow name is required", http.StatusBadGateway, 2002002)
	registerAdditionalError("at least one slot is required", http.StatusBadGateway, 2002003)
	registerAdditionalErrorPattern("slots[%d].id is required", "Generated workflow slot is missing an id", http.StatusBadGateway, 2002004)
	registerAdditionalError("at least one step is required", http.StatusBadGateway, 2002005)
	registerAdditionalErrorPattern("steps[%d].id is required", "Generated workflow step is missing an id", http.StatusBadGateway, 2002006)
	registerAdditionalError("scenario.md is empty", http.StatusBadGateway, 2002007)
	registerAdditionalError("scenario.md contains placeholder text", http.StatusBadGateway, 2002008)
	registerAdditionalError("state_yaml invalid while checking scenario.md", http.StatusBadGateway, 2002009)
	registerAdditionalErrorPattern("scenario.md does not document step %s", "Generated description is missing a workflow step", http.StatusBadGateway, 2002010)
	registerAdditionalErrorPattern("scenario.md has no description after step %s", "Generated workflow step description is missing", http.StatusBadGateway, 2002011)
	registerAdditionalErrorPattern("scenario.md description for step %s is too short", "Generated workflow step description is too short", http.StatusBadGateway, 2002012)
	registerAdditionalError("design brief is not available", http.StatusBadRequest, 2002013)
	registerAdditionalError("state machine is not valid", http.StatusBadRequest, 2002014)
	registerAdditionalErrorAlias("plugin", "Workflow operation failed", http.StatusInternalServerError, 2001818)
	registerAdditionalError("plugin mention is not accessible", http.StatusForbidden, 2001819)
	registerAdditionalErrorPattern("plugin revision %s has no compiled graph", "Workflow revision has no compiled graph", http.StatusInternalServerError, 2001820)
	registerAdditionalError("plugin source skill not found", http.StatusNotFound, 2001821)
	registerAdditionalError("plugin step was accepted but task lookup failed", http.StatusInternalServerError, 2001822)
	registerAdditionalError("workflow_id or step_id missing", http.StatusInternalServerError, 2001823)
	registerAdditionalError("workflow.yaml, state.yml and scenario.md are required", http.StatusBadRequest, 2001824)
	registerAdditionalError("precreate target document failed", http.StatusInternalServerError, 2001825)
	registerAdditionalErrorPattern("provider %q requires non-empty %s and %s descriptions", "Provider descriptions must not be empty", http.StatusBadRequest, 2001826)
	registerAdditionalError("provider name is required", http.StatusBadRequest, 2001827)
	registerAdditionalError("query reusable file failed", http.StatusInternalServerError, 2001828)
	registerAdditionalError("query uploaded files", http.StatusInternalServerError, 2001829)
	registerAdditionalError("question required", http.StatusBadRequest, 2001830)
	registerAdditionalError("question_type required", http.StatusBadRequest, 2001831)
	registerAdditionalError("question_type too long", http.StatusBadRequest, 2001832)
	registerAdditionalError("read import temp file", http.StatusInternalServerError, 2001833)
	registerAdditionalError("read uploaded text file", http.StatusInternalServerError, 2001834)
	registerAdditionalError("read zip file failed", http.StatusInternalServerError, 2001835)
	registerAdditionalError("redis ping failed", http.StatusInternalServerError, 2001836)
	registerAdditionalError("redis url scheme must be redis", http.StatusBadRequest, 2001837)
	registerAdditionalError("repair scenario", http.StatusInternalServerError, 2001838)
	registerAdditionalError("repair statemachine", http.StatusInternalServerError, 2001839)
	registerAdditionalError("repair validation failed", http.StatusInternalServerError, 2001840)
	registerAdditionalError("replace uploaded text file", http.StatusInternalServerError, 2001841)
	registerAdditionalError("request category and frontmatter category must match", http.StatusBadRequest, 2001842)
	registerAdditionalError("requestid is required", http.StatusBadRequest, 2001843)
	registerAdditionalError("resource has no base revision", http.StatusInternalServerError, 2001844)
	registerAdditionalError("resource has no head revision", http.StatusInternalServerError, 2001845)
	registerAdditionalError("resource update scanner db is nil", http.StatusInternalServerError, 2001846)
	registerAdditionalError("resource update scheduler db is nil", http.StatusInternalServerError, 2001847)
	registerAdditionalError("resource update worker db is nil", http.StatusInternalServerError, 2001848)
	registerAdditionalError("reusable file is not available", http.StatusBadRequest, 2001849)
	registerAdditionalError("reusable file metadata is invalid", http.StatusBadRequest, 2001850)
	registerAdditionalError("reusable file not found", http.StatusNotFound, 2001851)
	registerAdditionalError("review endpoint returned empty body", http.StatusBadRequest, 2001852)
	registerAdditionalErrorPattern("review endpoint returned http %d", "Review endpoint request failed", http.StatusBadGateway, 2001853)
	registerAdditionalError("review result conflict", http.StatusConflict, 2001854)
	registerAdditionalError("review result invalid", http.StatusBadRequest, 2001855)
	registerAdditionalError("review result not found", http.StatusNotFound, 2001856)
	registerAdditionalError("revision limit exceeded and no deletable revision found", http.StatusInternalServerError, 2001857)
	registerAdditionalError("revision ref requires skill_id and revision_id", http.StatusInternalServerError, 2001858)
	registerAdditionalErrorPattern("save artifact task=%s slot=%s seq=%d", "Failed to save the task artifact", http.StatusInternalServerError, 2001859)
	registerAdditionalError("save design_brief", http.StatusInternalServerError, 2001860)
	registerAdditionalError("save file failed", http.StatusInternalServerError, 2001861)
	registerAdditionalError("save repair", http.StatusInternalServerError, 2001862)
	registerAdditionalError("save scenario_scripts", http.StatusInternalServerError, 2001863)
	registerAdditionalError("save skeleton", http.StatusInternalServerError, 2001864)
	registerAdditionalError("save state_machine", http.StatusInternalServerError, 2001865)
	registerAdditionalErrorPattern("scan json from unsupported type %t", "Cannot read JSON from the supplied value type", http.StatusBadRequest, 2001866)
	registerAdditionalError("scan-control-plane request failed", http.StatusBadGateway, 2001867)
	registerAdditionalError("session graph hash mismatch", http.StatusBadRequest, 2001868)
	registerAdditionalError("session graph schema mismatch", http.StatusBadRequest, 2001869)
	registerAdditionalError("session not found or not dismissed", http.StatusNotFound, 2001870)
	registerAdditionalError("session_id, user_id, and last_message_id are required", http.StatusBadRequest, 2001871)
	registerAdditionalError("share item is not for target user", http.StatusForbidden, 2001872)
	registerAdditionalError("skill content must contain closing frontmatter separator", http.StatusBadRequest, 2001873)
	registerAdditionalError("skill draft changed before auto commit", http.StatusInternalServerError, 2001874)
	registerAdditionalError("skill has no base revision", http.StatusInternalServerError, 2001875)
	registerAdditionalError("skill has no head revision", http.StatusInternalServerError, 2001876)
	registerAdditionalError("skill market name already exists", http.StatusConflict, 2001877)
	registerAdditionalError("skill mention is not accessible or unpublished", http.StatusForbidden, 2001878)
	registerAdditionalError("skill name conflict", http.StatusConflict, 2001879)
	registerAdditionalError("skill package already exists", http.StatusConflict, 2001880)
	registerAdditionalError("skill package must contain skill.md", http.StatusBadRequest, 2001881)
	registerAdditionalError("skill path is required", http.StatusBadRequest, 2001882)
	registerAdditionalError("skill path must be skills/<category>/<skill_name>", http.StatusBadRequest, 2001883)
	registerAdditionalError("skill review history threshold not reached", http.StatusInternalServerError, 2001884)
	registerAdditionalError("skill review is too frequent", http.StatusInternalServerError, 2001885)
	registerAdditionalError("skill review window exceeds max window", http.StatusInternalServerError, 2001886)
	registerAdditionalError("skill review window is invalid", http.StatusBadRequest, 2001887)
	registerAdditionalError("skill scheduler active_task_id does not point to current task", http.StatusInternalServerError, 2001888)
	registerAdditionalError("skill_id and task_id are required", http.StatusBadRequest, 2001889)
	registerAdditionalErrorAlias("skill_id is required", "missing skill_id", http.StatusBadRequest, 2000854)
	registerAdditionalError("skill_ids is required", http.StatusBadRequest, 2001891)
	registerAdditionalError("skill.md frontmatter closing separator is required", http.StatusBadRequest, 2001892)
	registerAdditionalError("skill.md frontmatter field \"description\" is required", http.StatusBadRequest, 2001893)
	registerAdditionalError("skill.md frontmatter field \"name\" is required", http.StatusBadRequest, 2001894)
	registerAdditionalError("skill.md frontmatter is required", http.StatusBadRequest, 2001895)
	registerAdditionalError("skill.md must be a text file", http.StatusBadRequest, 2001896)
	registerAdditionalError("skills is required", http.StatusBadRequest, 2001897)
	registerAdditionalErrorPattern("skills must not exceed %d items", "Too many skills were provided", http.StatusBadRequest, 2001898)
	registerAdditionalError("soft delete uploaded file records", http.StatusInternalServerError, 2001899)
	registerAdditionalError("source and target path are the same", http.StatusBadRequest, 2001900)
	registerAdditionalError("source skill has no head revision", http.StatusInternalServerError, 2001901)
	registerAdditionalError("sqlite state init failed", http.StatusInternalServerError, 2001902)
	registerAdditionalError("stale draft version", http.StatusConflict, 2001903)
	registerAdditionalError("stale review version", http.StatusConflict, 2001904)
	registerAdditionalError("stat uploaded text file", http.StatusInternalServerError, 2001905)
	registerAdditionalError("state store is nil", http.StatusInternalServerError, 2001906)
	registerAdditionalErrorPattern("steps %s and %s belong to the same n-select-1 route from %s", "Transition steps cannot belong to the same exclusive route", http.StatusBadRequest, 2001907)
	registerAdditionalError("steps must be > 0", http.StatusBadRequest, 2001908)
	registerAdditionalError("store not initialised", http.StatusInternalServerError, 2001909)
	registerAdditionalError("stored_path must not be provided when an uploaded file is used", http.StatusBadRequest, 2001910)
	registerAdditionalError("stored_path required", http.StatusBadRequest, 2001911)
	registerAdditionalError("subagent run returned non-200", http.StatusBadGateway, 2001912)
	registerAdditionalErrorPattern("table %s missing columns", "Required database columns are missing", http.StatusNotFound, 2001913)
	registerAdditionalErrorPattern("tag exceeds max length of %d", "Tag exceeds the maximum length", http.StatusBadRequest, 2001914)
	registerAdditionalError("target folder not found", http.StatusNotFound, 2001915)
	registerAdditionalError("target kb id is empty", http.StatusBadRequest, 2001916)
	registerAdditionalError("target path already exists", http.StatusConflict, 2001917)
	registerAdditionalError("target_dataset_id is required", http.StatusBadRequest, 2001918)
	registerAdditionalError("target_dataset_id is required for copy/move task", http.StatusBadRequest, 2001919)
	registerAdditionalError("target_pid cannot be the same as source document", http.StatusBadRequest, 2001920)
	registerAdditionalError("target_pid must be a folder", http.StatusBadRequest, 2001921)
	registerAdditionalError("target_step_id is required for every target", http.StatusBadRequest, 2001922)
	registerAdditionalError("task guard db is nil", http.StatusInternalServerError, 2001923)
	registerAdditionalError("task_created event is missing task_id", http.StatusInternalServerError, 2001924)
	registerAdditionalError("text artifact value must contain text", http.StatusBadRequest, 2001925)
	registerAdditionalError("timed out waiting for migration lock", http.StatusInternalServerError, 2001926)
	registerAdditionalErrorPattern("too many tags, max is %d", "Too many tags were provided", http.StatusBadRequest, 2001927)
	registerAdditionalError("tool mention is not accessible", http.StatusForbidden, 2001928)
	registerAdditionalError("undo not available", http.StatusBadRequest, 2001929)
	registerAdditionalError("unknown hunk_id", http.StatusBadRequest, 2001930)
	registerAdditionalError("unmarshal evo response", http.StatusInternalServerError, 2001931)
	registerAdditionalError("unmarshal review response", http.StatusInternalServerError, 2001932)
	registerAdditionalErrorPattern("unsafe path %q", "unsafe path", http.StatusBadRequest, 2001547)
	registerAdditionalError("unsupported artifact content type", http.StatusBadRequest, 2001934)
	registerAdditionalError("unsupported compiled graph schema", http.StatusBadRequest, 2001935)
	registerAdditionalError("unsupported db_type", http.StatusBadRequest, 2001936)
	registerAdditionalErrorPattern("unsupported diff ref type %q", "Unsupported diff reference type", http.StatusBadRequest, 2001937)
	registerAdditionalError("unsupported driver", http.StatusBadRequest, 2001938)
	registerAdditionalError("unsupported mention type", http.StatusBadRequest, 2001939)
	registerAdditionalErrorPattern("unsupported ref type %q", "Unsupported reference type", http.StatusBadRequest, 2001940)
	registerAdditionalErrorPattern("unsupported resource type %q", "Unsupported resource type", http.StatusBadRequest, 2001941)
	registerAdditionalErrorPattern("unsupported review target %q", "Unsupported review target", http.StatusBadRequest, 2001942)
	registerAdditionalErrorPattern("unsupported skill operation %q", "Unsupported skill operation", http.StatusBadRequest, 2001943)
	registerAdditionalErrorPattern("unsupported source type %q", "Unsupported source type", http.StatusBadRequest, 2001944)
	registerAdditionalError("unsupported storage url", http.StatusBadRequest, 2001945)
	registerAdditionalError("unsupported task type", http.StatusBadRequest, 2001946)
	registerAdditionalError("update uploaded file state failed", http.StatusInternalServerError, 2001947)
	registerAdditionalError("upload file does not belong to current dataset", http.StatusInternalServerError, 2001948)
	registerAdditionalError("upload file does not belong to current user", http.StatusInternalServerError, 2001949)
	registerAdditionalError("upload file is not available for binding", http.StatusBadRequest, 2001950)
	registerAdditionalError("upload file not found", http.StatusNotFound, 2001951)
	registerAdditionalError("upload store does not implement get", http.StatusInternalServerError, 2001952)
	registerAdditionalError("upload store get has invalid signature", http.StatusBadRequest, 2001953)
	registerAdditionalError("upload store is not configured", http.StatusInternalServerError, 2001954)
	registerAdditionalError("upload store returned invalid session", http.StatusBadRequest, 2001955)
	registerAdditionalError("upload store returned non-error failure", http.StatusInternalServerError, 2001956)
	registerAdditionalError("upload stored_path not found", http.StatusNotFound, 2001957)
	registerAdditionalError("upload_file_id and content_hash cannot be set together", http.StatusBadRequest, 2001958)
	registerAdditionalError("upload_file_id is required", http.StatusBadRequest, 2001959)
	registerAdditionalError("upload_file_id is required for parse task", http.StatusBadRequest, 2001960)
	registerAdditionalError("upload_id required", http.StatusBadRequest, 2001961)
	registerAdditionalError("uploaded parts are incomplete", http.StatusInternalServerError, 2001962)
	registerAdditionalError("uploaded ref requires upload_id", http.StatusInternalServerError, 2001963)
	registerAdditionalError("uploaded text file encoding cannot be converted to utf-8", http.StatusBadRequest, 2001964)
	registerAdditionalErrorPattern("upstream %s returned %d", "Upstream service request failed", http.StatusBadGateway, 2001965)
	registerAdditionalErrorPattern("upstream context prompt returned status %d", "Upstream context prompt request failed", http.StatusBadGateway, 2001966)
	registerAdditionalErrorPattern("upstream context usage returned status %d", "Upstream context usage request failed", http.StatusBadGateway, 2001967)
	registerAdditionalError("url required", http.StatusBadRequest, 2001968)
	registerAdditionalError("user_preference content must contain closing frontmatter separator", http.StatusBadRequest, 2001969)
	registerAdditionalError("user_preference content must start with yaml frontmatter", http.StatusBadRequest, 2001970)
	registerAdditionalError("user_preference content or metadata required", http.StatusBadRequest, 2001971)
	registerAdditionalErrorPattern("user_preference frontmatter %s required", "Required user_preference frontmatter field is missing", http.StatusBadRequest, 2001972)
	registerAdditionalError("username required", http.StatusBadRequest, 2001973)
	registerAdditionalError("valid_rows mismatch", http.StatusBadRequest, 2001974)
	registerAdditionalErrorPattern("validate %s", "Database schema validation failed", http.StatusInternalServerError, 2001975)
	registerAdditionalErrorPattern("validate columns %s", "Database column validation failed", http.StatusInternalServerError, 2001976)
	registerAdditionalError("validate tool mention", http.StatusInternalServerError, 2001977)
	registerAdditionalError("versionfs draft base revision conflict", http.StatusConflict, 2001978)
	registerAdditionalError("versionfs draft is empty", http.StatusInternalServerError, 2001979)
	registerAdditionalError("versionfs head revision conflict", http.StatusConflict, 2001980)
	registerAdditionalError("versionfs stale draft version", http.StatusConflict, 2001981)
	registerAdditionalError("write paddleocr model field", http.StatusInternalServerError, 2001982)
	registerAdditionalError("write paddleocr optional payload", http.StatusInternalServerError, 2001983)
	registerAdditionalError("write skill zip failed", http.StatusInternalServerError, 2001984)
	registerAdditionalError("write upload meta failed", http.StatusInternalServerError, 2001985)
	registerAdditionalError("write utf-8 temp file", http.StatusInternalServerError, 2001986)
	registerAdditionalError("请先选择样本", http.StatusBadRequest, 2001987)
	registerAdditionalError("libreoffice is unavailable", http.StatusInternalServerError, 2001988)
	registerAdditionalErrorAlias("dataset name supports Chinese/English, numbers, -, _, ., up to 100 characters", "Invalid dataset name format", http.StatusBadRequest, 2001989)
	registerAdditionalError("cannot delete SKILL.md", http.StatusBadRequest, 2001990)
	registerAdditionalErrorAlias("-version v0_N is required for the structured migration layout", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("invalid version %q; expected v0_N", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorAlias("mode version must be > 0", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("dev version %d must be between 1 and %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("combined dev version v%d/%d exceeds bigint", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("load %s", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("version migration %d must be lower than %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("aggregate migration IDs must increase with release versions; %s has %d after %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("load %s/%s", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("%s must contain v0_N directories; found migration file %s", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("%s/%s must contain exactly one aggregate migration; found %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("%s/%s aggregate migration %d requires both up and down files", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("dev migration %s/%d must not declare Supersedes", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("open migration mode %s must be the latest mode", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("migration %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("migration %d supersedes non-lower version %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("invalid release version %q; expected v0_N", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("invalid release version %q", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("squash migration %d still has superseded migration file %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("duplicate migration history version %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("applied migration version %d has no migration file or release directory; refusing to execute SQL", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("migration %d has mixed aggregate and superseded history records", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("cannot canonicalize migration %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("cannot apply squash migration %d", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("migration mode %s has both aggregate version %d and dev migration history records", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorAlias("goto is not supported when dev_mode migrations are configured; use up or down", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("cannot bootstrap %s from combined dev version %d; restore its history records first", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorAlias("multiple Supersedes directives", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorAlias("empty Supersedes directive", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorAlias("invalid empty version in Supersedes directive", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("invalid version %q in Supersedes directive", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	registerAdditionalErrorPattern("duplicate version %d in Supersedes directive", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)

	// Stable aliases found by the repository-wide API/error-constructor audit.
	// Reuse the public generic codes while preserving the source text as Detail.
	registerAdditionalErrorPattern("call_mode must be '%s', '%s' or '%s'", "Invalid request", http.StatusBadRequest, 2000103)
	registerAdditionalErrorAlias("workflow is paused", "Conflict", http.StatusConflict, 2000107)
	for _, source := range []string{
		"invalid channel intent request", "invalid channel intent state", "invalid channel command registry",
		"invalid channel intent response", "basic chat does not support background execution",
		"basic chat does not support ask answers", "basic chat does not support plugin mentions",
		"conversation_id, decision_id and a valid action are required", "conversation and x-user-id are required",
		"invalid search config patch", "at most 20 knowledge bases are allowed",
		"selected_revision must be >= 1", "group name required", "multiple json values",
		"model max_input_tokens is only supported for llm, vlm, or embed models",
		"model max_input_tokens must be a positive integer or use a k or m suffix, for example 512, 128k, or 1m",
		"session_ids required", "invalid cadence expression", "invalid cadence metadata",
		"cadence interval must be between 1 and 52", "task description contains sensitive content",
		"task description contains sensitive word",
		"invalid artifact action preview request", "invalid artifact action target",
		"base_revision must be greater than zero", "invalid target_document",
		"invalid current writerdocument", "invalid resolved_media_assets",
		"file slot requires file or file_list content type", "invalid writerdocument state",
		"blocks must be an array", "source blocks must be an array",
		"invalid writer artifact", "writer artifact has no local path",
		"active draft_document must be an .lmd artifact",
		"writer artifact path is outside allowed storage", "read writer artifact",
		"active draft_document markdown is empty",
		"active draft_document must be an .lmd or .md artifact",
		"invalid writer download conversion key", "invalid writer download filename",
		"invalid writer download conversion request", "invalid writer download source format",
		"invalid writer download target format", "writer download conversion failed",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{
		"cannot disable workflows while a workflow is attached to the conversation", "revision conflict",
		"tool-limit decision is no longer active",
		"revision conflict; refresh and retry",
		"current draft_document revision is already synchronized",
		"initial feishu write-back has not completed",
		"invalid synchronized writerdocument baseline",
		"writerdocument identity does not match synchronized baseline",
		"synchronized baseline is not bound to a feishu document",
		"current writerdocument feishu binding does not match baseline",
		"task center is paused in settings",
		"skills and plugins are paused in settings", "workflows are paused in settings",
		"document parsing is paused in settings",
	} {
		registerAdditionalErrorAlias(source, "Conflict", http.StatusConflict, 2000107)
	}
	registerAdditionalErrorAlias("knowledge base is not readable", "forbidden", http.StatusForbidden, 2000102)
	registerAdditionalErrorAlias("workflow not found", "Resource not found", http.StatusNotFound, 2000408)
	for _, source := range []string{
		"workflow session not found", "selected artifact not found",
		"writer session not found", "active draft_document not found",
		"writer download conversion not found",
	} {
		registerAdditionalErrorAlias(source, "Resource not found", http.StatusNotFound, 2000106)
	}
	registerAdditionalErrorAlias("feishu authorization required", "unauthorized", http.StatusUnauthorized, 2000104)
	registerAdditionalErrorAlias("dataset_ids is required", "dataset_ids required", http.StatusBadRequest, 2001349)
	for _, source := range []string{
		"no chat model configured", "failed to deliver tool-limit decision", "update search config failed",
		"marshal writerdocument artifact failed", "artifact save failed", "decrypt api key failed",
		"encrypt api key failed", "failed to create waiting task",
		"invalid workflow action response",
		"unsupported model provider credential ciphertext", "decode sensitive-word check",
		"built-in workflow package directory not found", "workflow.yaml missing from revision",
		"pin legacy workflow session revision", "resolve conversation plugin binding failed",
		"decode conversation ext",
		"load artifact action head revision", "parse artifact action policy",
		"artifact action head revision is incomplete",
		"decode sync_document action response", "artifact sync state save failed",
		"invalid render response",
		"task unavailable",
		"query task center settings failed", "query settings controls failed",
		"query document parsing settings failed",
		"query writer download conversion failed", "writer download conversion path is invalid",
		"open writer download conversion failed", "read writer download conversion failed",
		"save writer download conversion failed", "index writer download conversion failed",
		"encode writer download conversion request failed",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
	for _, source := range []string{
		"channel intent classification failed", "writer document sync failed", "sensitive-word check unavailable",
		"sensitive-word check failed",
		"workflow artifact action failed",
		"writer document write-back failed",
		"render writer document failed",
		"writer download conversion service unavailable",
	} {
		registerAdditionalErrorAlias(source, "Upstream service error", http.StatusBadGateway, 2000110)
	}
	registerAdditionalErrorPattern("chat service returned status %d", "Upstream service error", http.StatusBadGateway, 2000110)
	registerAdditionalErrorPattern("migrate model provider credential %s", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorPattern("load workflow head revision %s", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorPattern("session_ids must belong to user %q and must not contain plugin conversations", "Invalid request", http.StatusBadRequest, 2000103)
	registerAdditionalErrorAlias("cron expression produces no future times within 5 years", "Invalid request", http.StatusBadRequest, 2000103)
	registerAdditionalErrorAlias("cron expression has no previous time within 5 years", "Invalid request", http.StatusBadRequest, 2000103)
	registerAdditionalErrorPattern("no sql block for database dialect %q", "Database migration configuration is invalid", http.StatusBadRequest, 2001991)
	for _, source := range []string{
		"invalid source_type", "skill_id, revision_id and tree_hash are required",
		"invalid package", "workflow id is required",
		"attempt_id and operation are required",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	registerAdditionalErrorPattern("seed builtin workflow %s", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorPattern("lazymind host execution failed: %s", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorAlias("lazymind host execution failed", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorPattern("save sources task=%s", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorPattern("invalid sources snapshot", "Internal server error", http.StatusInternalServerError, 2000000)
	for _, source := range []string{
		"chat service returned no run terminal",
		"invalid algorithm stream frame",
		"read algorithm stream",
		"algorithm emitted payload after run_finished",
		"algorithm combined run_finished with business payload",
		"algorithm emitted duplicate run_finished",
		"algorithm stream ended without run_finished",
		"runtime event is nil",
		"invalid runtime event envelope",
		"runtime event run_id mismatch",
		"unsupported runtime event type",
		"invalid model_retry_scheduled data",
		"model_retry_scheduled fields are required",
		"invalid model_retry_scheduled values",
		"invalid model_call_finished data",
		"model_call_finished fields are required",
		"model_call_finished finish outcome is invalid",
		"unsupported model finish",
		"model_call_finished failure outcome is invalid",
		"unsupported model failure origin",
		"unsupported model failure code",
		"unsupported model_call_finished kind",
		"runtime event is not run_finished",
		"invalid run_finished data",
		"run_finished partial_output is required",
		"run_finished partial_output must be boolean",
		"run_finished code must be a string",
		"invalid run status/reason combination",
	} {
		registerAdditionalErrorAlias(source, "algorithm chat stream failed", http.StatusBadGateway, 2002077)
	}
}

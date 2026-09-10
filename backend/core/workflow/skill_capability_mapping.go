package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/workflow/graphengine"
)

type skillCapabilityRequirement struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	WorkflowCapability string   `json:"workflow_capability"`
	FrameworkTool      string   `json:"framework_tool,omitempty"`
	WorkflowTools      []string `json:"workflow_tools,omitempty"`
	Supported          bool     `json:"supported"`
	Required           bool     `json:"required"`
	Configurable       bool     `json:"configurable,omitempty"`
	ConfigWarning      bool     `json:"config_warning,omitempty"`
	CredentialKind     string   `json:"credential_kind,omitempty"`
	Reason             string   `json:"reason"`
	Keywords           []string `json:"-"`
}

type skillCapabilityRuntimeIssue struct {
	Capability string
	Label      string
	Reason     string
}

var skillCapabilityCatalog = []skillCapabilityRequirement{
	{ID: "skillhub_search", Label: "SkillHub 搜索", WorkflowCapability: "http_request", WorkflowTools: []string{"url_fetch"}, Supported: true, Required: true, Reason: "Skill 需要访问 SkillHub 公开接口或技能市场数据。", Keywords: []string{"skillhub", "skillhub.cn", "api.skillhub.cn", "技能市场", "搜索 skill", "检索 skill", "查找 skill"}},
	{ID: "public_api_access", Label: "公开 API 访问", WorkflowCapability: "http_request", WorkflowTools: []string{"url_fetch"}, Supported: true, Required: true, Reason: "Skill 需要访问明确的 URL 或公开 HTTP API。", Keywords: []string{"http://", "https://", "url fetch", "web fetch", "网页读取", "读取网页", "抓取网页", "访问网页", "打开链接", "读取 url", "fetch url", "http get", "requests.get", "curl "}},
	{ID: "authenticated_api_access", Label: "凭证型 API 访问", WorkflowCapability: "credentialed_http_request", WorkflowTools: []string{"url_fetch"}, Supported: true, Required: true, Configurable: true, ConfigWarning: true, CredentialKind: "api_key", Reason: "Skill 需要 API Key、Token、OAuth 或其他用户凭证。", Keywords: []string{"authorization header", "authorization:", "x-api-key", "bearer token", "access token", "oauth", "client secret", "api key in", "api key for", "with an api key", "requires api key", "api key required", "api key is required", "api key configured", "configured api key", "user-provided api key", "api token", "requires authentication", "requires auth", "需要 api key", "填写 api key", "配置 api key", "未配置 api key", "需要 token", "填写 token", "配置 token", "访问令牌", "授权令牌", "用户凭证", "鉴权凭证"}},
	{ID: "web_search", Label: "网页搜索", WorkflowCapability: "web_search", FrameworkTool: "web_search", WorkflowTools: []string{"web_search"}, Supported: true, Required: true, Reason: "Skill 需要搜索引擎检索网页或实时信息。", Keywords: []string{"web search", "internet search", "联网搜索", "网络搜索", "网页搜索", "搜索网页", "全网搜索", "实时搜索", "google search", "bing search", "bocha", "博查", "tavily"}},
	{ID: "academic_search", Label: "学术搜索", WorkflowCapability: "academic_search", FrameworkTool: "academic_search", WorkflowTools: []string{"academic_search"}, Supported: true, Required: true, Reason: "Skill 需要论文、文献或学术检索能力。", Keywords: []string{"academic search", "scholar", "论文检索", "学术搜索", "文献检索", "arxiv", "pubmed", "semantic scholar", "sciverse"}},
	{ID: "cloud_files", Label: "云文档/网盘", WorkflowCapability: "cloud_files", FrameworkTool: "cloud_files", WorkflowTools: []string{"cloud_files"}, Supported: true, Required: true, Reason: "Skill 需要访问 LazyMind 已接入的云文档或网盘。", Keywords: []string{"google drive", "googledrive", "notion", "飞书", "feishu", "云文档", "网盘", "drive 文件"}},
	{ID: "text2image", Label: "文生图", WorkflowCapability: "text2image", WorkflowTools: []string{"image_generator"}, Supported: true, Required: true, Reason: "Skill 需要文生图模型能力。", Keywords: []string{"text2image", "text-to-image", "generate image", "generate images", "image generation", "文生图", "生成图片", "生成图像", "出图", "ai 绘图"}},
	{ID: "image_editing", Label: "图片编辑", WorkflowCapability: "image_editing", WorkflowTools: []string{"image_editor"}, Supported: true, Required: true, Reason: "Skill 需要图片编辑模型能力。", Keywords: []string{"image editing", "image_editing", "图片编辑", "编辑图片", "改图", "修图", "局部重绘"}},
	{ID: "vlm", Label: "图片理解", WorkflowCapability: "vlm", WorkflowTools: []string{"multimodal"}, Supported: true, Required: true, Reason: "Skill 需要图片理解或视觉模型能力。", Keywords: []string{"vision", "vlm", "inspect image", "inspect images", "uploaded image", "uploaded images", "image understanding", "图片理解", "图像理解", "识图", "看图", "图片解析", "图像解析", "ocr"}},
}

var wordBoundaryCapabilityTokens = regexp.MustCompile(`[a-z0-9_./:-]+`)

func detectSkillCapabilityRequirementsFromSnapshot(snapshot workflowSourceSkillSnapshot) []skillCapabilityRequirement {
	var parts []string
	for _, file := range snapshot.Files {
		if file.Binary {
			continue
		}
		switch {
		case file.Path == "SKILL.md" || strings.HasPrefix(file.Path, "scripts/"):
			parts = append(parts, file.Path, file.Content)
		case strings.HasPrefix(file.Path, "references/"):
			if content := executableReferenceCapabilityContent(file.Content); content != "" {
				parts = append(parts, file.Path, content)
			}
		}
	}
	return detectSkillCapabilityRequirements(strings.Join(parts, "\n"))
}

func executableReferenceCapabilityContent(content string) string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if referenceCapabilityExecutionLine(line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func referenceCapabilityExecutionLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	executionHints := []string{
		"api", "http://", "https://", "curl", "requests.", "fetch(", "http get",
		"endpoint", "tool", "command", "cli", "调用", "执行", "运行", "安装", "接口", "工具", "命令",
	}
	for _, hint := range executionHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func skillPackageFiles(pkg map[string]any) []skillPackageFile {
	rawFiles, _ := pkg["files"].([]any)
	files := make([]skillPackageFile, 0, len(rawFiles))
	for _, raw := range rawFiles {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		file := skillPackageFile{
			Path:     stringMapValue(item, "path"),
			BlobHash: stringMapValue(item, "blob_hash"),
			Mime:     stringMapValue(item, "mime"),
			FileType: stringMapValue(item, "file_type"),
		}
		if content, ok := item["content"].(string); ok {
			file.Content = content
		}
		if size, ok := item["size"].(float64); ok {
			file.Size = int64(size)
		} else if size, ok := item["size"].(int64); ok {
			file.Size = size
		} else if size, ok := item["size"].(int); ok {
			file.Size = int64(size)
		}
		if binary, ok := item["binary"].(bool); ok {
			file.Binary = binary
		}
		if file.Path != "" {
			files = append(files, file)
		}
	}
	return files
}

func stringMapValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return stringAny(value)
}

func stringAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func detectSkillCapabilityRequirements(content string) []skillCapabilityRequirement {
	lower := strings.ToLower(content)
	tokens := map[string]bool{}
	for _, token := range wordBoundaryCapabilityTokens.FindAllString(lower, -1) {
		tokens[token] = true
	}
	out := make([]skillCapabilityRequirement, 0)
	seenCapability := map[string]bool{}
	for _, item := range skillCapabilityCatalog {
		mentioned := skillCapabilityMentioned(lower, tokens, item.Keywords)
		if item.ID == "authenticated_api_access" {
			mentioned = credentialCapabilityMentioned(content, item.Keywords)
		}
		if !mentioned {
			continue
		}
		if seenCapability[item.WorkflowCapability] {
			continue
		}
		seenCapability[item.WorkflowCapability] = true
		copied := item
		copied.Keywords = nil
		out = append(out, copied)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].WorkflowCapability < out[j].WorkflowCapability })
	return out
}

func credentialCapabilityMentioned(content string, keywords []string) bool {
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || credentialReferenceOnlyLine(lower) {
			continue
		}
		lineTokens := map[string]bool{}
		for _, token := range wordBoundaryCapabilityTokens.FindAllString(lower, -1) {
			lineTokens[token] = true
		}
		if skillCapabilityMentioned(lower, lineTokens, keywords) {
			return true
		}
	}
	return false
}

func credentialReferenceOnlyLine(lower string) bool {
	ignored := []string{
		"requires_api_key",
		"无需鉴权",
		"无需 api key",
		"不需要 api key",
		"不需要api key",
		"无需 key",
		"不需要 key",
		"no api key",
		"without api key",
		"does not require api key",
		"doesn't require api key",
	}
	for _, phrase := range ignored {
		if strings.Contains(lower, phrase) {
			if strings.Contains(lower, "if ") || strings.Contains(lower, "missing") || strings.Contains(lower, "configured") || strings.Contains(lower, "provided") || strings.Contains(lower, "未配置") {
				return false
			}
			return true
		}
	}
	return false
}

func skillCapabilityMentioned(lower string, tokens map[string]bool, keywords []string) bool {
	for _, keyword := range keywords {
		needle := strings.ToLower(strings.TrimSpace(keyword))
		if needle == "" {
			continue
		}
		if strings.ContainsAny(needle, " -./:") {
			if strings.Contains(lower, needle) {
				return true
			}
			continue
		}
		if !capabilityKeywordNeedsWordBoundary(needle) && strings.Contains(lower, needle) {
			return true
		}
		if tokens[needle] {
			return true
		}
	}
	return false
}

func capabilityKeywordNeedsWordBoundary(keyword string) bool {
	for _, r := range keyword {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func mergeDetectedCapabilityMappings(existing map[string]any, detected []skillCapabilityRequirement) map[string]any {
	if existing == nil {
		existing = map[string]any{}
	}
	for _, req := range detected {
		key := "capability:" + req.WorkflowCapability
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = map[string]any{
			"action":              "require",
			"required":            req.Required,
			"capability":          req.ID,
			"workflow_capability": req.WorkflowCapability,
			"framework_tool":      req.FrameworkTool,
			"workflow_tools":      req.WorkflowTools,
			"available":           req.Supported,
			"configurable":        req.Configurable,
			"config_warning":      req.ConfigWarning,
			"credential_kind":     req.CredentialKind,
			"label":               req.Label,
			"reason":              req.Reason,
			"source":              "deterministic_skill_capability_scan",
		}
	}
	return existing
}

func reconcileDetectedCapabilityMappings(existing map[string]any, detected []skillCapabilityRequirement) map[string]any {
	if existing == nil {
		existing = map[string]any{}
	}
	allowed := map[string]bool{}
	for _, req := range detected {
		if req.WorkflowCapability != "" {
			allowed[req.WorkflowCapability] = true
		}
	}
	for key, raw := range existing {
		item, _ := raw.(map[string]any)
		capability := stringAny(item["workflow_capability"])
		if capability == "" && strings.HasPrefix(key, "capability:") {
			capability = strings.TrimPrefix(key, "capability:")
		}
		if !skillCapabilityMappingEntry(key, item, capability) {
			continue
		}
		if !allowed[capability] {
			delete(existing, key)
		}
	}
	return mergeDetectedCapabilityMappings(existing, detected)
}

func skillCapabilityMappingEntry(key string, item map[string]any, capability string) bool {
	if strings.HasPrefix(key, "capability:") {
		return true
	}
	if stringAny(item["action"]) != "require" || capability == "" {
		return false
	}
	return knownSkillWorkflowCapability(capability)
}

func knownSkillWorkflowCapability(capability string) bool {
	for _, req := range skillCapabilityCatalog {
		if req.WorkflowCapability == capability {
			return true
		}
	}
	return false
}

func detectedCapabilitiesFromMappings(mappings map[string]any) []skillCapabilityRequirement {
	var out []skillCapabilityRequirement
	seen := map[string]bool{}
	for _, raw := range mappings {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		required, _ := item["required"].(bool)
		if !required && item["action"] != "require" {
			continue
		}
		capability := stringAny(item["workflow_capability"])
		if capability == "" {
			capability = stringAny(item["framework_tool"])
		}
		if capability == "" || seen[capability] {
			continue
		}
		available, ok := item["available"].(bool)
		if !ok {
			available = false
		}
		seen[capability] = true
		out = append(out, skillCapabilityRequirement{
			ID:                 stringAny(item["capability"]),
			Label:              stringAny(item["label"]),
			WorkflowCapability: capability,
			FrameworkTool:      stringAny(item["framework_tool"]),
			WorkflowTools:      workflowToolsFromMappingItem(item, capability),
			Supported:          available,
			Required:           true,
			Configurable:       boolAny(item["configurable"]),
			ConfigWarning:      boolAny(item["config_warning"]),
			CredentialKind:     stringAny(item["credential_kind"]),
			Reason:             stringAny(item["reason"]),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].WorkflowCapability < out[j].WorkflowCapability })
	return out
}

func boolAny(value any) bool {
	v, _ := value.(bool)
	return v
}

func workflowToolsFromMappingItem(item map[string]any, capability string) []string {
	tools := stringSliceAny(item["workflow_tools"])
	if len(tools) > 0 {
		return tools
	}
	return defaultWorkflowToolsForCapability(capability)
}

func defaultWorkflowToolsForCapability(capability string) []string {
	switch strings.TrimSpace(capability) {
	case "http_request", "credentialed_http_request":
		return []string{"url_fetch"}
	case "web_search":
		return []string{"web_search"}
	case "academic_search":
		return []string{"academic_search"}
	case "cloud_files":
		return []string{"cloud_files"}
	case "vlm":
		return []string{"multimodal"}
	case "text2image":
		return []string{"image_generator"}
	case "image_editing":
		return []string{"image_editor"}
	default:
		return nil
	}
}

func stringSliceAny(value any) []string {
	var out []string
	add := func(raw any) {
		text := stringAny(raw)
		if text != "" {
			out = append(out, text)
		}
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			add(item)
		}
	case []string:
		for _, item := range v {
			add(item)
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			add(part)
		}
	}
	return uniqueSortedStrings(out)
}

func injectSkillCapabilitiesIntoWorkflow(workflowYAML, stateYAML string, mappings map[string]any) (string, string, []string) {
	requirements := detectedCapabilitiesFromMappings(mappings)
	var capabilities []string
	var tools []string
	for _, req := range requirements {
		if req.Supported && req.WorkflowCapability != "" {
			capabilities = append(capabilities, req.WorkflowCapability)
			tools = append(tools, req.WorkflowTools...)
		}
	}
	capabilities = uniqueSortedStrings(capabilities)
	tools = uniqueSortedStrings(tools)
	nextWorkflow, workflowChanged := workflowYAML, false
	if withCredentialFields, changed := injectCredentialClarificationFields(nextWorkflow, requirements); changed {
		nextWorkflow = withCredentialFields
		workflowChanged = true
	}
	if len(capabilities) == 0 && len(tools) == 0 {
		if !workflowChanged {
			return workflowYAML, stateYAML, nil
		}
		return nextWorkflow, stateYAML, capabilities
	}
	stepTools := stepWorkflowToolsFromMappings(mappings, requirements)
	nextState, stateChanged := injectCapabilitiesIntoStateSteps(stateYAML, capabilities)
	if withTools, changed := injectToolsIntoStateSteps(nextState, tools, stepTools); changed {
		nextState = withTools
		stateChanged = true
	}
	if !workflowChanged && !stateChanged {
		return workflowYAML, stateYAML, nil
	}
	return nextWorkflow, nextState, capabilities
}

func injectCredentialClarificationFields(content string, requirements []skillCapabilityRequirement) (string, bool) {
	var doc map[string]any
	if yaml.Unmarshal([]byte(content), &doc) != nil || doc == nil {
		return content, false
	}
	var credentialRequirements []skillCapabilityRequirement
	for _, req := range requirements {
		if req.Configurable && req.CredentialKind != "" {
			credentialRequirements = append(credentialRequirements, req)
		}
	}
	if len(credentialRequirements) == 0 {
		return content, false
	}
	runtime, _ := doc["runtime"].(map[string]any)
	if runtime == nil {
		runtime = map[string]any{}
	}
	fields, _ := runtime["clarification_fields"].([]any)
	seen := map[string]bool{}
	for _, raw := range fields {
		field, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := stringAny(field["id"])
		if id != "" {
			seen[id] = true
		}
	}
	changed := false
	for _, req := range credentialRequirements {
		id := req.ID + "_credential"
		if seen[id] {
			continue
		}
		fields = append(fields, map[string]any{
			"id":       id,
			"label":    req.Label + "配置",
			"question": fmt.Sprintf("请填写%s所需的 API Key、Token、OAuth 或其他凭证；如果暂时没有，可以留空后在运行前补充。", req.Label),
			"type":     "text",
		})
		seen[id] = true
		changed = true
	}
	if !changed {
		return content, false
	}
	runtime["clarification_fields"] = fields
	doc["runtime"] = runtime
	out, err := yaml.Marshal(doc)
	if err != nil {
		return content, false
	}
	return string(out), true
}

func injectCapabilitiesIntoStateSteps(content string, capabilities []string) (string, bool) {
	var doc map[string]any
	if yaml.Unmarshal([]byte(content), &doc) != nil || doc == nil {
		return content, false
	}
	rawSteps, ok := doc["steps"]
	if !ok {
		return content, false
	}
	changed := false
	switch steps := rawSteps.(type) {
	case map[string]any:
		for _, raw := range steps {
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			step["capabilities"] = mergeStringListAny(step["capabilities"], capabilities)
			changed = true
		}
	case []any:
		for _, raw := range steps {
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			step["capabilities"] = mergeStringListAny(step["capabilities"], capabilities)
			changed = true
		}
	}
	if !changed {
		return content, false
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return content, false
	}
	return string(out), true
}

func stepWorkflowToolsFromMappings(mappings map[string]any, requirements []skillCapabilityRequirement) map[string][]string {
	capabilityTools := map[string][]string{}
	for _, req := range requirements {
		if req.WorkflowCapability != "" {
			capabilityTools[req.WorkflowCapability] = req.WorkflowTools
		}
		if req.FrameworkTool != "" && len(req.WorkflowTools) > 0 {
			capabilityTools[req.FrameworkTool] = req.WorkflowTools
		}
		if req.ID != "" && len(req.WorkflowTools) > 0 {
			capabilityTools[req.ID] = req.WorkflowTools
		}
	}
	out := map[string][]string{}
	for key, raw := range mappings {
		if strings.HasPrefix(key, "capability:") || strings.HasPrefix(key, "script:") {
			continue
		}
		stepID := strings.TrimSpace(key)
		if stepID == "" {
			continue
		}
		var tools []string
		switch item := raw.(type) {
		case string:
			tools = capabilityTools[strings.TrimSpace(item)]
		case map[string]any:
			if mappedTools := stringSliceAny(item["workflow_tools"]); len(mappedTools) > 0 {
				tools = mappedTools
				break
			}
			for _, field := range []string{"workflow_capability", "framework_tool", "capability", "tool"} {
				if mapped := capabilityTools[stringAny(item[field])]; len(mapped) > 0 {
					tools = mapped
					break
				}
			}
		}
		if len(tools) > 0 {
			out[stepID] = uniqueSortedStrings(append(out[stepID], tools...))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func injectToolsIntoStateSteps(content string, fallbackTools []string, stepTools map[string][]string) (string, bool) {
	var doc map[string]any
	if yaml.Unmarshal([]byte(content), &doc) != nil || doc == nil {
		return content, false
	}
	rawSteps, ok := doc["steps"]
	if !ok {
		return content, false
	}
	toolsForStep := func(stepID string) []string {
		if len(stepTools) > 0 {
			return stepTools[stepID]
		}
		return fallbackTools
	}
	changed := false
	switch steps := rawSteps.(type) {
	case map[string]any:
		for stepID, raw := range steps {
			tools := toolsForStep(stepID)
			if len(tools) == 0 {
				continue
			}
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			step["tools"] = mergeStringListAny(step["tools"], tools)
			changed = true
		}
	case []any:
		for _, raw := range steps {
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			stepID := stringAny(step["id"])
			tools := toolsForStep(stepID)
			if len(tools) == 0 {
				continue
			}
			step["tools"] = mergeStringListAny(step["tools"], tools)
			changed = true
		}
	}
	if !changed {
		return content, false
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return content, false
	}
	return string(out), true
}

const workflowExecutionBoundaryMarker = "Workflow execution boundaries:"

type workflowExecutionBoundaryKind string

const (
	workflowBoundarySearch    workflowExecutionBoundaryKind = "search"
	workflowBoundaryHTTP      workflowExecutionBoundaryKind = "http"
	workflowBoundaryFile      workflowExecutionBoundaryKind = "file"
	workflowBoundaryImage     workflowExecutionBoundaryKind = "image"
	workflowBoundaryCode      workflowExecutionBoundaryKind = "code"
	workflowBoundaryKnowledge workflowExecutionBoundaryKind = "knowledge"
	workflowBoundaryPlatform  workflowExecutionBoundaryKind = "platform"
)

func injectExecutionBoundariesIntoStateSteps(content string) (string, bool) {
	var doc map[string]any
	if yaml.Unmarshal([]byte(content), &doc) != nil || doc == nil {
		return content, false
	}
	rawSteps, ok := doc["steps"]
	if !ok {
		return content, false
	}
	changed := false
	switch steps := rawSteps.(type) {
	case map[string]any:
		for stepID, raw := range steps {
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if appendExecutionBoundariesToStep(stepID, step) {
				changed = true
			}
		}
	case []any:
		for _, raw := range steps {
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if appendExecutionBoundariesToStep(stringAny(step["id"]), step) {
				changed = true
			}
		}
	}
	if !changed {
		return content, false
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return content, false
	}
	return string(out), true
}

func appendExecutionBoundariesToStep(stepID string, step map[string]any) bool {
	kinds := workflowExecutionBoundaryKinds(stepID, stringAny(step["prompt"]), stringSliceAny(step["capabilities"]), stringSliceAny(step["tools"]), stringSliceAny(step["terminal_tools"]))
	if len(kinds) == 0 {
		return false
	}
	prompt := strings.TrimSpace(stringAny(step["prompt"]))
	if strings.Contains(prompt, workflowExecutionBoundaryMarker) {
		return false
	}
	step["prompt"] = appendWorkflowExecutionBoundaryPrompt(prompt, kinds)
	return true
}

func appendWorkflowExecutionBoundaryPrompt(prompt string, kinds []workflowExecutionBoundaryKind) string {
	block := workflowExecutionBoundaryPrompt(kinds)
	if strings.TrimSpace(prompt) == "" {
		return block
	}
	return strings.TrimSpace(prompt) + "\n\n" + block
}

func workflowExecutionBoundaryPrompt(kinds []workflowExecutionBoundaryKind) string {
	kinds = uniqueWorkflowBoundaryKinds(kinds)
	lines := []string{
		workflowExecutionBoundaryMarker,
		"- Treat explicitly provided user inputs and upstream artifacts as the required scope; do not truncate them because of default exploration limits.",
		"- Limit only extra exploration such as search, pagination, discovery, parameter probing, generated alternatives, and retries.",
		"- Stop as soon as the declared output has enough valid results for the requested target count or quality threshold.",
		"- Do not keep calling tools only to make the result more exhaustive after the target is satisfied.",
		"- If the budget is exhausted or some items fail, save the best available partial result with skipped items and failure reasons.",
		"- Before finishing this step, write every declared output artifact, even when the result is partial or empty.",
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundarySearch) {
		lines = append(lines,
			"- Search budget: use at most 6 search calls. Try the strongest query first, then at most 2 additional query variants only if the target is not met.",
		)
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundaryHTTP) {
		lines = append(lines,
			"- HTTP/API budget: use at most 12 extra HTTP calls. Fetch at most 2 pages per query or endpoint, and try at most 1 fallback parameter strategy before moving on.",
		)
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundaryFile) {
		lines = append(lines,
			"- File budget: process all explicitly provided files or upstream file artifacts. Do not recursively discover extra files beyond the stated task unless required.",
		)
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundaryImage) {
		lines = append(lines,
			"- Image budget: process all explicitly provided images or upstream image artifacts. Retry a failed image operation at most once, then record the failure and continue.",
		)
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundaryCode) {
		lines = append(lines,
			"- Code/script budget: run commands at most 3 times and attempt at most 2 fixes. If still failing, save diagnostics instead of continuing to patch/run indefinitely.",
		)
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundaryKnowledge) {
		lines = append(lines,
			"- Retrieval budget: use at most 4 retrieval rounds with focused queries. Stop when enough evidence is available for the declared output.",
		)
	}
	if containsWorkflowBoundaryKind(kinds, workflowBoundaryPlatform) {
		lines = append(lines,
			"- Platform-tool budget: use at most 10 extra platform queries and at most 3 pages. Stop immediately on permission or credential failures and save an actionable limitation.",
		)
	}
	return strings.Join(lines, "\n")
}

func workflowExecutionBoundaryKinds(stepID, prompt string, capabilities, tools, terminalTools []string) []workflowExecutionBoundaryKind {
	stepText := strings.ToLower(strings.Join([]string{stepID, prompt}, "\n"))
	toolParts := append([]string{}, capabilities...)
	toolParts = append(toolParts, tools...)
	toolParts = append(toolParts, terminalTools...)
	toolText := strings.ToLower(strings.Join(toolParts, "\n"))
	added := map[workflowExecutionBoundaryKind]bool{}
	var out []workflowExecutionBoundaryKind
	add := func(kind workflowExecutionBoundaryKind) {
		if !added[kind] {
			added[kind] = true
			out = append(out, kind)
		}
	}
	addFromText := func(text string) {
		if containsAnyBoundaryToken(text, "web_search", "academic_search", "search", "搜索", "检索", "查找", "全网", "实时信息", "scholar", "pubmed", "arxiv") {
			add(workflowBoundarySearch)
		}
		if containsAnyBoundaryToken(text, "url_fetch", "http_request", "credentialed_http_request", "http://", "https://", "api", "endpoint", "fetch", "curl", "分页", "page", "抓取", "访问网页", "读取网页", "接口") {
			add(workflowBoundaryHTTP)
		}
		if containsAnyBoundaryToken(text, "cloud_files", "read_file", "write_file", "file", "document", "文件", "文档", "目录", "附件") {
			add(workflowBoundaryFile)
		}
		if containsAnyBoundaryToken(text, "vlm", "multimodal", "image_generator", "image_editor", "text2image", "image_editing", "image", "ocr", "图片", "图像", "识图", "看图", "文生图", "改图") {
			add(workflowBoundaryImage)
		}
		if containsAnyBoundaryToken(text, "run_script", "terminal", "python", "shell", "script_runtime", "script", "command", "test", "build", "脚本", "命令", "运行", "测试", "修复") {
			add(workflowBoundaryCode)
		}
		if containsAnyBoundaryToken(text, "knowledge", "knowledge_search", "kb", "rag", "知识库", "召回", "证据", "检索资料") {
			add(workflowBoundaryKnowledge)
		}
		if containsAnyBoundaryToken(text, "feishu", "notion", "googledrive", "google drive", "tencent", "meeting", "calendar", "platform", "云文档", "网盘", "飞书", "腾讯", "会议") {
			add(workflowBoundaryPlatform)
		}
	}
	addFromText(stepText)
	if strings.TrimSpace(toolText) == "" || !workflowStepSuggestsOpenToolUse(stepText) {
		return out
	}
	if containsAnyBoundaryToken(toolText, "web_search", "academic_search", "search", "scholar", "pubmed", "arxiv") {
		add(workflowBoundarySearch)
	}
	if containsAnyBoundaryToken(toolText, "url_fetch", "http_request", "credentialed_http_request", "api", "fetch", "curl") {
		add(workflowBoundaryHTTP)
	}
	if containsAnyBoundaryToken(toolText, "cloud_files", "read_file", "write_file", "file", "document") {
		add(workflowBoundaryFile)
	}
	if containsAnyBoundaryToken(toolText, "vlm", "multimodal", "image_generator", "image_editor", "text2image", "image_editing", "image", "ocr") {
		add(workflowBoundaryImage)
	}
	if containsAnyBoundaryToken(toolText, "run_script", "terminal", "python", "shell", "script_runtime", "script", "command", "test", "build") {
		add(workflowBoundaryCode)
	}
	if containsAnyBoundaryToken(toolText, "knowledge", "knowledge_search", "kb", "rag") {
		add(workflowBoundaryKnowledge)
	}
	if containsAnyBoundaryToken(toolText, "feishu", "notion", "googledrive", "google drive", "tencent", "meeting", "calendar", "platform") {
		add(workflowBoundaryPlatform)
	}
	return out
}

func workflowStepSuggestsOpenToolUse(text string) bool {
	return strings.TrimSpace(text) == "" || containsAnyBoundaryToken(text,
		"search", "find", "lookup", "fetch", "get", "collect", "gather", "crawl", "scrape", "retrieve", "query", "call", "request", "download",
		"analyze", "inspect", "process", "extract", "read", "write", "generate", "create", "edit", "convert", "run", "execute", "test", "build", "repair",
		"搜索", "检索", "查找", "获取", "抓取", "访问", "调用", "请求", "下载", "分析", "解析", "处理", "提取", "读取", "写入", "生成", "创建", "编辑", "转换", "运行", "执行", "测试", "修复",
	)
}

func containsAnyBoundaryToken(text string, tokens ...string) bool {
	for _, token := range tokens {
		if boundaryTokenMatches(text, token) {
			return true
		}
	}
	return false
}

func boundaryTokenMatches(text, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	if asciiWordToken(token) {
		pattern := `(^|[^a-z0-9_])` + regexp.QuoteMeta(token) + `([^a-z0-9_]|$)`
		return regexp.MustCompile(pattern).FindStringIndex(text) != nil
	}
	return strings.Contains(text, token)
}

func asciiWordToken(token string) bool {
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func uniqueWorkflowBoundaryKinds(kinds []workflowExecutionBoundaryKind) []workflowExecutionBoundaryKind {
	seen := map[workflowExecutionBoundaryKind]bool{}
	var out []workflowExecutionBoundaryKind
	for _, kind := range kinds {
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

func containsWorkflowBoundaryKind(kinds []workflowExecutionBoundaryKind, want workflowExecutionBoundaryKind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func mergeStringListAny(existing any, values []string) []any {
	seen := map[string]bool{}
	var out []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	switch v := existing.(type) {
	case []any:
		for _, item := range v {
			add(fmt.Sprint(item))
		}
	case []string:
		for _, item := range v {
			add(item)
		}
	case string:
		add(v)
	}
	for _, value := range values {
		add(value)
	}
	sort.Strings(out)
	result := make([]any, len(out))
	for i := range out {
		result[i] = out[i]
	}
	return result
}

func requiredCapabilityPublishDiagnostics(db *gorm.DB, draft orm.WorkflowDraft, compiled graphengine.CompileResult) []authoringDiagnostic {
	if strings.TrimSpace(draft.SourceAnalysisID) == "" {
		return nil
	}
	var analysis orm.WorkflowGenerationAnalysis
	if err := db.Where("id = ? AND draft_id = ?", draft.SourceAnalysisID, draft.ID).First(&analysis).Error; err != nil {
		return nil
	}
	var mappings map[string]any
	if json.Unmarshal([]byte(analysis.ToolMappingReportJSON), &mappings) != nil {
		return []authoringDiagnostic{{Code: "SKILL_CAPABILITY_MAPPING_INVALID", Severity: "error", Message: "Skill capability mapping report is invalid"}}
	}
	requirements := detectedCapabilitiesFromMappings(mappings)
	if len(requirements) == 0 {
		return nil
	}
	if compiled.Graph == nil {
		return []authoringDiagnostic{{Code: "SKILL_CAPABILITY_GRAPH_UNAVAILABLE", Severity: "error", Path: "scenario/state.yml.steps.tools", Message: "compiled Workflow graph is unavailable for required Skill capability/tool validation"}}
	}
	declared := map[string]bool{}
	declaredTools := map[string]bool{}
	for _, node := range compiled.Graph.Nodes {
		for _, capability := range node.Capabilities {
			declared[capability] = true
		}
		for _, tool := range node.LegacyTools {
			declaredTools[tool] = true
		}
	}
	var out []authoringDiagnostic
	for _, req := range requirements {
		if !req.Supported {
			out = append(out, authoringDiagnostic{Code: "SKILL_CAPABILITY_UNSUPPORTED", Severity: "error", Path: "scenario/state.yml.steps.tools", Message: fmt.Sprintf("required Skill capability is not supported by Workflow conversion: %s", req.Label)})
			continue
		}
		if req.ConfigWarning {
			out = append(out, authoringDiagnostic{Code: "WORKFLOW_CREDENTIAL_CONFIGURATION_RECOMMENDED", Severity: "warning", Path: "workflow.yaml.runtime.clarification_fields", Message: fmt.Sprintf("required Skill capability may need user credentials configured before execution: %s", req.Label)})
		}
		if !requiredCapabilityDeclared(req, declared, declaredTools) {
			out = append(out, authoringDiagnostic{Code: "SKILL_CAPABILITY_NOT_DECLARED", Severity: "error", Path: "scenario/state.yml.steps.tools", Message: fmt.Sprintf("required Skill capability is not declared by Workflow tools/capabilities: %s", req.WorkflowCapability)})
		}
	}
	return out
}

func workflowRevisionCapabilityDiagnostics(compiledJSON []byte, mappings map[string]any) (bool, string) {
	requirements := detectedCapabilitiesFromMappings(mappings)
	if len(requirements) == 0 {
		return true, ""
	}
	var graph graphengine.CompiledStateGraph
	if len(compiledJSON) == 0 || json.Unmarshal(compiledJSON, &graph) != nil {
		return false, "workflow_projection_unavailable"
	}
	declared := map[string]bool{}
	declaredTools := map[string]bool{}
	for _, node := range graph.Nodes {
		for _, capability := range node.Capabilities {
			declared[capability] = true
		}
		for _, tool := range node.LegacyTools {
			declaredTools[tool] = true
		}
	}
	for _, req := range requirements {
		if !req.Supported {
			return false, "required_capability_unsupported"
		}
		if !requiredCapabilityDeclared(req, declared, declaredTools) {
			return false, "required_capability_missing"
		}
	}
	return true, ""
}

func requiredCapabilityDeclared(req skillCapabilityRequirement, declaredCapabilities, declaredTools map[string]bool) bool {
	if declaredCapabilities[req.WorkflowCapability] {
		return true
	}
	for _, tool := range req.WorkflowTools {
		if declaredTools[tool] {
			return true
		}
	}
	for _, tool := range defaultWorkflowToolsForCapability(req.WorkflowCapability) {
		if declaredTools[tool] {
			return true
		}
	}
	return false
}

func skillCapabilityRuntimeConfigIssues(ctx context.Context, db *gorm.DB, userID string, requirements []skillCapabilityRequirement) []skillCapabilityRuntimeIssue {
	requirements = supportedRequiredCapabilities(requirements)
	if len(requirements) == 0 {
		return nil
	}
	var issues []skillCapabilityRuntimeIssue
	toolCapabilities := workflowToolConfigCapabilityNames(requirements)
	if len(toolCapabilities) > 0 {
		toolConfig, toolErr := modelconfig.LoadToolConfigForCapabilities(ctx, db, userID, toolCapabilities)
		if toolErr != nil {
			for _, req := range requirements {
				if workflowCapabilityUsesToolConfig(req.WorkflowCapability) {
					issues = append(issues, skillCapabilityRuntimeIssue{Capability: req.WorkflowCapability, Label: req.Label, Reason: "tool_config_check_failed"})
				}
			}
		} else {
			for _, req := range requirements {
				if !workflowCapabilityUsesToolConfig(req.WorkflowCapability) {
					continue
				}
				if !workflowCapabilityToolConfigPresent(req.WorkflowCapability, toolConfig) {
					issues = append(issues, skillCapabilityRuntimeIssue{Capability: req.WorkflowCapability, Label: req.Label, Reason: "tool_config_missing"})
				}
			}
		}
	}

	if requirementsIncludeModelConfig(requirements) {
		llmConfig, llmErr := modelconfig.LoadLLMConfig(ctx, db, userID)
		if llmErr != nil {
			for _, req := range requirements {
				if workflowCapabilityUsesModelConfig(req.WorkflowCapability) {
					issues = append(issues, skillCapabilityRuntimeIssue{Capability: req.WorkflowCapability, Label: req.Label, Reason: "model_config_check_failed"})
				}
			}
		} else {
			for _, req := range requirements {
				if !workflowCapabilityUsesModelConfig(req.WorkflowCapability) {
					continue
				}
				if !workflowCapabilityModelConfigPresent(req.WorkflowCapability, llmConfig) {
					issues = append(issues, skillCapabilityRuntimeIssue{Capability: req.WorkflowCapability, Label: req.Label, Reason: "model_config_missing"})
				}
			}
		}
	}
	return issues
}

func supportedRequiredCapabilities(requirements []skillCapabilityRequirement) []skillCapabilityRequirement {
	out := make([]skillCapabilityRequirement, 0, len(requirements))
	seen := map[string]bool{}
	for _, req := range requirements {
		if !req.Required || !req.Supported || strings.TrimSpace(req.WorkflowCapability) == "" || seen[req.WorkflowCapability] {
			continue
		}
		seen[req.WorkflowCapability] = true
		out = append(out, req)
	}
	return out
}

func workflowToolConfigCapabilityNames(requirements []skillCapabilityRequirement) []string {
	var names []string
	for _, req := range requirements {
		if workflowCapabilityUsesToolConfig(req.WorkflowCapability) {
			names = append(names, req.WorkflowCapability)
		}
	}
	return uniqueSortedStrings(names)
}

func workflowCapabilityUsesToolConfig(capability string) bool {
	switch strings.TrimSpace(capability) {
	case "web_search", "academic_search", "cloud_files":
		return true
	default:
		return false
	}
}

func workflowCapabilityUsesModelConfig(capability string) bool {
	switch strings.TrimSpace(capability) {
	case "vlm", "text2image", "image_editing":
		return true
	default:
		return false
	}
}

func requirementsIncludeModelConfig(requirements []skillCapabilityRequirement) bool {
	for _, req := range requirements {
		if workflowCapabilityUsesModelConfig(req.WorkflowCapability) {
			return true
		}
	}
	return false
}

func workflowCapabilityToolConfigPresent(capability string, toolConfig map[string]any) bool {
	switch strings.TrimSpace(capability) {
	case "web_search":
		return anyConfigKeyPresent(toolConfig, "google", "bocha", "bing", "tavily")
	case "academic_search":
		return anyConfigKeyPresent(toolConfig, "sciverse")
	case "cloud_files":
		return anyConfigKeyPresent(toolConfig, "feishu", "googledrive", "notion")
	default:
		return true
	}
}

func workflowCapabilityModelConfigPresent(capability string, llmConfig map[string]any) bool {
	switch strings.TrimSpace(capability) {
	case "vlm":
		return anyConfigKeyPresent(llmConfig, "vlm")
	case "text2image":
		return anyConfigKeyPresent(llmConfig, "image_generator", "text2image")
	case "image_editing":
		return anyConfigKeyPresent(llmConfig, "image_editor", "image_editing", "image_generator")
	default:
		return true
	}
}

func anyConfigKeyPresent(config map[string]any, keys ...string) bool {
	for _, key := range keys {
		if config != nil && config[key] != nil {
			return true
		}
	}
	return false
}

func workflowRuntimeCapabilityDiagnostics(ctx context.Context, db *gorm.DB, userID string, mappings map[string]any) (bool, string) {
	issues := skillCapabilityRuntimeConfigIssues(ctx, db, userID, detectedCapabilitiesFromMappings(mappings))
	if len(issues) == 0 {
		return true, ""
	}
	return false, "required_capability_config_missing:" + issues[0].Capability
}

func requiredCapabilityMappingsForDraft(db *gorm.DB, draftID, analysisID string) map[string]any {
	if strings.TrimSpace(analysisID) == "" {
		return nil
	}
	var analysis orm.WorkflowGenerationAnalysis
	if err := db.Where("id = ? AND draft_id = ?", analysisID, draftID).First(&analysis).Error; err != nil {
		return nil
	}
	var mappings map[string]any
	if json.Unmarshal([]byte(analysis.ToolMappingReportJSON), &mappings) != nil {
		return nil
	}
	return mappings
}

func latestRequiredCapabilityMappingsForDraft(db *gorm.DB, draftID string) map[string]any {
	if strings.TrimSpace(draftID) == "" {
		return nil
	}
	var analysis orm.WorkflowGenerationAnalysis
	if err := db.Where("draft_id = ?", draftID).Order("created_at DESC").First(&analysis).Error; err != nil {
		return nil
	}
	var mappings map[string]any
	if json.Unmarshal([]byte(analysis.ToolMappingReportJSON), &mappings) != nil {
		return nil
	}
	return mappings
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

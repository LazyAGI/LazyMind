package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/plugin"
)

type chatMention struct {
	MentionID   string `json:"mention_id"`
	Type        string `json:"type"`
	ResourceID  string `json:"resource_id"`
	DisplayName string `json:"display_name"`
	Start       *int   `json:"start,omitempty"`
	End         *int   `json:"end,omitempty"`
}

type resolvedChatMentions struct {
	PluginRefs          []string
	SkillNames          []string
	KnowledgeBaseIDs    []string
	ToolNames           []string
	ExcludedToolNames   []string
	ExcludedPluginRefs  []string
	ConversationContext string
	ResourceMentions    []map[string]string
}

var mentionDenyWords = []string{
	"不要使用", "不要调用", "不要启用", "不要用", "不要", "别用", "别使用", "别调用",
	"不想使用", "不想调用", "不想用", "不使用", "不用", "无需", "不能调用", "不能启用",
	"不能用", "不能使用", "禁止使用", "禁止调用", "避免使用", "排除", "忽略", "跳过",
	"do not use", "don't use", "dont use", "never use", "without", "exclude", "ignore", "avoid",
}

var mentionAllowWords = []string{
	"可以使用", "可以用", "可使用", "可用", "请使用", "请用", "优先使用", "允许使用",
	"使用", "调用", "启用", "can use", "may use", "please use", "use", "enable",
}

// mentionIsDenied only examines the local clause immediately before this mention.
// Comparing the nearest positive and negative cue prevents a denial for @x from
// leaking into a later "可以用 @y" clause.
func mentionIsDenied(query string, mention chatMention) bool {
	queryRunes := []rune(query)
	position := -1
	if mention.Start != nil && *mention.Start >= 0 && *mention.Start <= len(queryRunes) {
		position = *mention.Start
	}
	if position < 0 && strings.TrimSpace(mention.DisplayName) != "" {
		position = strings.Index(strings.ToLower(query), strings.ToLower(mention.DisplayName))
		if position >= 0 {
			position = len([]rune(query[:position]))
		}
	}
	if position < 0 {
		return false
	}
	start := position - 40
	if start < 0 {
		start = 0
	}
	prefix := strings.ToLower(string(queryRunes[start:position]))
	for _, separator := range []string{"，", ",", "。", "；", ";", "！", "!", "？", "?", "\n", "但是", "不过", "然而", "但"} {
		if index := strings.LastIndex(prefix, separator); index >= 0 {
			prefix = prefix[index+len(separator):]
		}
	}
	lastDeny, lastAllow := -1, -1
	for _, word := range mentionDenyWords {
		if index := strings.LastIndex(prefix, word); index >= 0 && index+len(word) > lastDeny {
			lastDeny = index + len(word)
		}
	}
	for _, word := range mentionAllowWords {
		if index := strings.LastIndex(prefix, word); index >= 0 && index+len(word) > lastAllow {
			lastAllow = index + len(word)
		}
	}
	return lastDeny >= 0 && lastDeny >= lastAllow
}

func parseChatMentions(raw map[string]any) ([]chatMention, error) {
	value, ok := raw["mentions"]
	if !ok || value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("invalid mentions")
	}
	var mentions []chatMention
	if err := json.Unmarshal(b, &mentions); err != nil {
		return nil, fmt.Errorf("invalid mentions")
	}
	seen := map[string]struct{}{}
	out := make([]chatMention, 0, len(mentions))
	for _, mention := range mentions {
		mention.Type = strings.TrimSpace(mention.Type)
		mention.ResourceID = strings.TrimSpace(mention.ResourceID)
		if mention.ResourceID == "" {
			return nil, fmt.Errorf("mention resource_id required")
		}
		key := mention.Type + "\x00" + mention.ResourceID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, mention)
	}
	return out, nil
}

func applyChatMentions(ctx context.Context, db *gorm.DB, raw map[string]any, userID, convID, sessionID, query string, resources *evolution.ChatResourceContext) (string, resolvedChatMentions, error) {
	mentions, err := parseChatMentions(raw)
	if err != nil || len(mentions) == 0 {
		return query, resolvedChatMentions{}, err
	}
	resolved := resolvedChatMentions{}
	var datasetIDs, skillIDs, conversationIDs []string
	for _, mention := range mentions {
		denied := (mention.Type == "tool" || mention.Type == "plugin") && mentionIsDenied(query, mention)
		switch mention.Type {
		case "knowledge_base":
			if !acl.Can(userID, acl.ResourceTypeDB, mention.ResourceID, acl.PermRead) {
				return query, resolved, fmt.Errorf("knowledge base mention is not readable: %s", mention.ResourceID)
			}
			var count int64
			if err := db.WithContext(ctx).Model(&orm.Dataset{}).Where("id = ? AND deleted_at IS NULL", mention.ResourceID).Count(&count).Error; err != nil || count == 0 {
				return query, resolved, fmt.Errorf("knowledge base mention not found: %s", mention.ResourceID)
			}
			datasetIDs = append(datasetIDs, mention.ResourceID)
			resolved.KnowledgeBaseIDs = append(resolved.KnowledgeBaseIDs, mention.ResourceID)
			resolved.ResourceMentions = append(resolved.ResourceMentions, map[string]string{
				"resource_type": "knowledge_base", "resource_ref": mention.ResourceID,
				"display_name": mention.DisplayName,
			})
		case "skill":
			var skill orm.SkillV2Skill
			if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", mention.ResourceID, userID).Take(&skill).Error; err != nil || skill.HeadRevisionID == nil {
				return query, resolved, fmt.Errorf("skill mention is not accessible or unpublished: %s", mention.ResourceID)
			}
			skillIDs = append(skillIDs, mention.ResourceID)
			resolved.SkillNames = append(
				resolved.SkillNames,
				fmt.Sprintf("%s/%s", strings.TrimSpace(skill.Category), strings.TrimSpace(skill.SkillName)),
			)
			resolved.ResourceMentions = append(resolved.ResourceMentions, map[string]string{
				"resource_type": "skill",
				"resource_ref":  resolved.SkillNames[len(resolved.SkillNames)-1],
				"display_name":  mention.DisplayName,
			})
		case "tool":
			tools, toolErr := fetchChatTools(ctx, db, userID, "")
			if toolErr != nil {
				return query, resolved, fmt.Errorf("validate tool mention: %w", toolErr)
			}
			if _, ok := findToolGroup(tools.ToolGroups, mention.ResourceID); !ok {
				return query, resolved, fmt.Errorf("tool mention is not accessible: %s", mention.ResourceID)
			}
			if denied {
				resolved.ExcludedToolNames = append(resolved.ExcludedToolNames, mention.ResourceID)
			} else {
				resolved.ToolNames = append(resolved.ToolNames, mention.ResourceID)
			}
		case "plugin":
			if strings.HasPrefix(mention.ResourceID, "builtin:") {
				if denied {
					resolved.ExcludedPluginRefs = append(resolved.ExcludedPluginRefs, mention.ResourceID)
					continue
				}
				resolved.PluginRefs = append(resolved.PluginRefs, mention.ResourceID)
				resolved.ResourceMentions = append(resolved.ResourceMentions, map[string]string{
					"resource_type": "plugin", "resource_ref": mention.ResourceID,
					"display_name": mention.DisplayName,
				})
				continue
			}
			var count int64
			if err := db.WithContext(ctx).Model(&orm.PluginResource{}).
				Where("plugin_ref = ? AND status = 'active' AND (owner_user_id = ? OR owner_user_id = '')", mention.ResourceID, userID).Count(&count).Error; err != nil || count == 0 {
				return query, resolved, fmt.Errorf("plugin mention is not accessible: %s", mention.ResourceID)
			}
			if denied {
				resolved.ExcludedPluginRefs = append(resolved.ExcludedPluginRefs, mention.ResourceID)
				continue
			}
			resolved.PluginRefs = append(resolved.PluginRefs, mention.ResourceID)
			resolved.ResourceMentions = append(resolved.ResourceMentions, map[string]string{
				"resource_type": "plugin", "resource_ref": mention.ResourceID,
				"display_name": mention.DisplayName,
			})
		case "conversation":
			conversationIDs = append(conversationIDs, mention.ResourceID)
		default:
			return query, resolved, fmt.Errorf("unsupported mention type: %s", mention.Type)
		}
	}

	if len(skillIDs) > 0 {
		if err := evolution.AddMentionedSkills(ctx, db, userID, sessionID, skillIDs, resources); err != nil {
			return query, resolved, err
		}
	}
	if len(datasetIDs) > 0 {
		mergeMentionedDatasets(raw, datasetIDs)
	}
	if len(conversationIDs) > 3 {
		return query, resolved, fmt.Errorf("at most 3 conversation mentions are allowed")
	}
	if len(conversationIDs) > 0 {
		contextText, err := mentionedConversationContext(ctx, db, userID, convID, conversationIDs)
		if err != nil {
			return query, resolved, err
		}
		resolved.ConversationContext = contextText
	}
	return query, resolved, nil
}

func applyExplicitResourceBindings(body map[string]any, mentions resolvedChatMentions) {
	if len(mentions.SkillNames) == 0 && len(mentions.KnowledgeBaseIDs) == 0 && len(mentions.PluginRefs) == 0 {
		return
	}
	body["explicit_resource_bindings"] = map[string]any{
		"skill_names":        mentions.SkillNames,
		"knowledge_base_ids": mentions.KnowledgeBaseIDs,
		"plugin_refs":        mentions.PluginRefs,
		"mentions":           mentions.ResourceMentions,
	}
}

func mergeMentionedDatasets(raw map[string]any, ids []string) {
	conversation, _ := raw["conversation"].(map[string]any)
	if conversation == nil {
		conversation = map[string]any{}
		raw["conversation"] = conversation
	}
	search, _ := conversation["search_config"].(map[string]any)
	if search == nil {
		search = map[string]any{}
		conversation["search_config"] = search
	}
	list, _ := search["dataset_list"].([]any)
	seen := map[string]bool{}
	for _, item := range list {
		if value, ok := item.(map[string]any); ok {
			seen[strings.TrimSpace(fmt.Sprint(value["id"]))] = true
		}
	}
	for _, id := range ids {
		if !seen[id] {
			list = append(list, map[string]any{"id": id})
		}
	}
	search["dataset_list"] = list
}

const recentMentionTurnLimit = 6
const recentMentionResourceLimit = 10

// buildMentionResourceContext gives the model an unambiguous display-name to
// resource-id mapping without persisting that internal context in chat history.
// Recent mentions are informational only: they never alter filters or enabled tools.
func buildMentionResourceContext(ctx context.Context, db *gorm.DB, userID string, histories []orm.ChatHistory, raw map[string]any) string {
	current, _ := parseChatMentions(raw)
	recent := recentHistoryMentions(histories, recentMentionTurnLimit)
	if len(current) == 0 && len(recent) == 0 {
		return ""
	}

	toolIDs := map[string]bool{}
	needsTools := false
	for _, mention := range append(append([]chatMention{}, current...), recent...) {
		if mention.Type == "tool" {
			needsTools = true
			break
		}
	}
	if needsTools {
		if tools, err := fetchChatTools(ctx, db, userID, ""); err == nil {
			for _, group := range tools.ToolGroups {
				toolIDs[strings.TrimSpace(fmt.Sprint(group["name"]))] = true
			}
		}
	}
	isReadable := func(mention chatMention) bool {
		switch mention.Type {
		case "knowledge_base":
			if !acl.Can(userID, acl.ResourceTypeDB, mention.ResourceID, acl.PermRead) {
				return false
			}
			var count int64
			return db.WithContext(ctx).Model(&orm.Dataset{}).Where("id = ? AND deleted_at IS NULL", mention.ResourceID).Count(&count).Error == nil && count > 0
		case "skill":
			var count int64
			return db.WithContext(ctx).Model(&orm.SkillV2Skill{}).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", mention.ResourceID, userID).Count(&count).Error == nil && count > 0
		case "plugin":
			if strings.HasPrefix(mention.ResourceID, "builtin:") {
				return true
			}
			var count int64
			return db.WithContext(ctx).Model(&orm.PluginResource{}).Where("plugin_ref = ? AND status = 'active' AND (owner_user_id = ? OR owner_user_id = '')", mention.ResourceID, userID).Count(&count).Error == nil && count > 0
		case "tool":
			return toolIDs[mention.ResourceID]
		case "conversation":
			var count int64
			return db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", mention.ResourceID, userID).Count(&count).Error == nil && count > 0
		default:
			return false
		}
	}

	seen := map[string]bool{}
	filter := func(items []chatMention, limit int) []chatMention {
		out := make([]chatMention, 0, len(items))
		for _, mention := range items {
			key := mention.Type + "\x00" + mention.ResourceID
			if seen[key] || !isReadable(mention) {
				continue
			}
			seen[key] = true
			out = append(out, mention)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out
	}
	current = filter(current, 0)
	recent = filter(recent, recentMentionResourceLimit)
	if len(current) == 0 && len(recent) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "<mentioned_resources>", "The following resources were explicitly referenced by the user. Names are for interpretation; use resource IDs for calls.")
	if len(current) > 0 {
		lines = append(lines, "Current-turn references (authorized for this turn):")
		for _, mention := range current {
			lines = append(lines, fmt.Sprintf("- type=%s, name=%q, id=%q", mention.Type, mention.DisplayName, mention.ResourceID))
		}
	}
	if len(recent) > 0 {
		lines = append(lines, "Recent references (context only; do not use them to expand filters, permissions, or enabled tools):")
		for _, mention := range recent {
			lines = append(lines, fmt.Sprintf("- type=%s, name=%q, id=%q", mention.Type, mention.DisplayName, mention.ResourceID))
		}
	}
	lines = append(lines, "</mentioned_resources>")
	return strings.Join(lines, "\n")
}

func recentHistoryMentions(histories []orm.ChatHistory, turnLimit int) []chatMention {
	if turnLimit <= 0 || len(histories) == 0 {
		return nil
	}
	start := len(histories) - turnLimit
	if start < 0 {
		start = 0
	}
	var out []chatMention
	for i := len(histories) - 1; i >= start; i-- {
		if len(histories[i].Ext) == 0 {
			continue
		}
		var ext struct {
			Mentions []chatMention `json:"mentions"`
		}
		if json.Unmarshal(histories[i].Ext, &ext) == nil {
			out = append(out, ext.Mentions...)
		}
	}
	return out
}

func mentionedConversationContext(ctx context.Context, db *gorm.DB, userID, currentID string, ids []string) (string, error) {
	var chunks []string
	for _, id := range ids {
		var conversation orm.Conversation
		if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", id, userID).Take(&conversation).Error; err != nil {
			return "", fmt.Errorf("conversation mention is not readable: %s", id)
		}
		if id == currentID {
			return "", fmt.Errorf("cannot mention the current conversation")
		}
		var histories []orm.ChatHistory
		if err := db.WithContext(ctx).Where("conversation_id = ?", id).Order("seq DESC").Limit(6).Find(&histories).Error; err != nil {
			return "", err
		}
		for left, right := 0, len(histories)-1; left < right; left, right = left+1, right-1 {
			histories[left], histories[right] = histories[right], histories[left]
		}
		var lines []string
		for _, history := range histories {
			lines = append(lines, "User: "+history.Content, "Assistant: "+history.Result)
		}
		chunks = append(chunks, "Conversation "+conversation.DisplayName+":\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(chunks, "\n\n"), nil
}

func applyMentionedTools(disabled []string, enabled []string) []string {
	allow := map[string]bool{}
	for _, name := range enabled {
		allow[name] = true
	}
	out := disabled[:0]
	for _, name := range disabled {
		if !allow[name] {
			out = append(out, name)
		}
	}
	return out
}

func mergeMentionedPlugins(ctx context.Context, db *gorm.DB, userID string, refs []string, catalog []map[string]any) ([]map[string]any, []string, error) {
	if len(refs) == 0 {
		return catalog, nil, nil
	}
	byRef := map[string]map[string]any{}
	for _, item := range catalog {
		byRef[fmt.Sprint(item["plugin_ref"])] = item
	}
	selected := make([]map[string]any, 0, len(refs))
	var forcedBuiltins []string
	for _, ref := range refs {
		if strings.HasPrefix(ref, "builtin:") {
			forcedBuiltins = append(forcedBuiltins, strings.TrimPrefix(ref, "builtin:"))
			continue
		}
		if item, ok := byRef[ref]; ok {
			selected = append(selected, item)
			continue
		}
		var row struct {
			orm.PluginResource
			TreeHash string `gorm:"column:tree_hash"`
		}
		if err := db.WithContext(ctx).Table("plugins p").Select("p.*, pr.tree_hash").
			Joins("JOIN plugin_revisions pr ON pr.id=p.head_revision_id").
			Where("p.plugin_ref=? AND p.status='active' AND (p.owner_user_id=? OR p.owner_user_id='')", ref, userID).Take(&row).Error; err != nil {
			return nil, nil, fmt.Errorf("plugin mention is not accessible: %s", ref)
		}
		selected = append(selected, map[string]any{"plugin_ref": row.PluginRef, "plugin_id": row.PluginID, "name": row.Name, "description": row.Description, "when_to_use": row.WhenToUse, "source_type": row.SourceType, "remote_root": "remote://" + row.RelativeRoot, "revision_id": row.HeadRevisionID, "revision_no": row.Version, "tree_hash": row.TreeHash})
	}
	return selected, forcedBuiltins, nil
}

// applyPluginSelection is the single catalog/allowlist assembly path used by
// both real chat execution and context preview/export.
func applyPluginSelection(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	reqBody map[string]any,
	mentionedRefs []string,
	excludedRefs []string,
) error {
	if len(mentionedRefs) > 1 {
		return fmt.Errorf("at most one plugin mention is allowed per turn")
	}
	if len(mentionedRefs) > 0 {
		reqBody["enable_plugin"] = true
		reqBody["allowed_plugin_refs"] = mentionedRefs
	}
	if enabled, _ := reqBody["enable_plugin"].(bool); !enabled {
		reqBody["plugin_catalog"] = []map[string]any{}
		reqBody["disabled_builtin_plugins"] = []string{}
		return nil
	}
	catalog, err := plugin.EnabledCatalog(db, userID)
	if err != nil {
		return fmt.Errorf("load plugin catalog: %w", err)
	}
	excluded := map[string]bool{}
	for _, ref := range excludedRefs {
		excluded[ref] = true
	}
	filteredCatalog := catalog[:0]
	for _, item := range catalog {
		if !excluded[fmt.Sprint(item["plugin_ref"])] {
			filteredCatalog = append(filteredCatalog, item)
		}
	}
	catalog = filteredCatalog
	catalog, forcedBuiltins, err := mergeMentionedPlugins(
		ctx, db, userID, mentionedRefs, catalog,
	)
	if err != nil {
		return err
	}
	disabledBuiltins, err := plugin.DisabledBuiltinPluginIDs(db, userID)
	if err != nil {
		return fmt.Errorf("load builtin plugin settings: %w", err)
	}
	for _, ref := range excludedRefs {
		if strings.HasPrefix(ref, "builtin:") {
			disabledBuiltins = append(disabledBuiltins, strings.TrimPrefix(ref, "builtin:"))
		}
	}
	if pluginContext, ok := reqBody["plugin_context"].(map[string]any); ok {
		if excluded[fmt.Sprint(pluginContext["plugin_ref"])] {
			reqBody["plugin_context"] = map[string]any{}
		}
	}
	reqBody["plugin_catalog"] = catalog
	reqBody["disabled_builtin_plugins"] = applyMentionedTools(
		disabledBuiltins, forcedBuiltins,
	)
	return nil
}

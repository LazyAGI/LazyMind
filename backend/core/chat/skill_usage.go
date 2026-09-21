package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
)

const conversationSkillUsageKey = "skill_usage"

type conversationSkillUsage struct {
	SelectedIDs []string                `json:"selected_ids"`
	ExcludedIDs []string                `json:"excluded_ids"`
	Invocations []evolution.LoadedSkill `json:"invocations,omitempty"`
}

var explicitSkillCue = regexp.MustCompile(`(?i)(?:请使用|请用|使用|调用|启用|不要使用|不要调用|不要用|不用|别用|禁止使用|排除|忽略|跳过|取消(?:使用|调用)?|停止(?:使用|调用)|停用|禁用|\b(?:please\s+use|use|enable|do\s+not\s+use|don't\s+use|without|exclude|ignore|disable|cancel|stop\s+using))\s*[\x60"'@]*$`)

const globalSkillDenyAction = "skill_deny_all"

// Match direct clauses only: questions such as “为什么不用任何 Skill” are not commands.
var globalSkillDenyCue = regexp.MustCompile(`(?i)(?:^|[，,。.;；！!\n])\s*(?:(?:请|之后|然后|后来|现在|please|then)\s*)*(?:(?:不要(?:使用|调用|用)|不用|禁止(?:使用|调用)|禁用)\s*(?:(?:任何|所有|全部)(?:的)?\s*)?(?:skills?|技能)|(?:do\s+not\s+use|don't\s+use|never\s+use|without|disable)\s+(?:(?:any|all)\s+)?skills|no\s+skills)`)

func globalSkillDenyMentions(query string) []chatMention {
	var actions []chatMention
	for _, match := range globalSkillDenyCue.FindAllStringIndex(query, -1) {
		suffix := query[match[1]:]
		if suffix != "" {
			next := []rune(suffix)[0]
			if unicode.IsLetter(next) || unicode.IsDigit(next) || strings.ContainsRune("_-/", next) {
				continue
			}
		}
		// A question in the same clause is informational, not an explicit opt-out.
		clause := suffix
		if end := strings.IndexFunc(clause, func(r rune) bool { return strings.ContainsRune("，,。.;；！!\n", r) }); end >= 0 {
			clause = clause[:end]
		}
		if strings.ContainsAny(clause, "?？") {
			continue
		}
		position := len([]rune(query[:match[1]]))
		actions = append(actions, chatMention{Type: globalSkillDenyAction, Start: &position})
	}
	return actions
}

// Exact names require an adjacent invocation cue. A bare name in quoted content
// or a longer identifier must not authorize a manual-only skill.
func explicitSkillMentions(query string, skills []orm.SkillV2Skill, bound ...chatMention) ([]chatMention, error) {
	type match struct {
		mention    chatMention
		start, end int
	}
	var matches []match
	lower := strings.ToLower(query)
	for _, skill := range skills {
		for _, label := range []string{skill.Category + "/" + skill.SkillName, skill.SkillName} {
			if label == "" {
				continue
			}
			needle := strings.ToLower(label)
			for offset := 0; offset < len(lower); {
				index := strings.Index(lower[offset:], needle)
				if index < 0 {
					break
				}
				start := offset + index
				end := start + len(needle)
				offset = end
				if end < len(lower) {
					next := []rune(lower[end:])[0]
					if next < 128 && (unicode.IsLetter(next) || unicode.IsDigit(next) || strings.ContainsRune("_-/", next)) {
						continue
					}
				}
				prefix := query[:start]
				if !explicitSkillCue.MatchString(prefix) {
					continue
				}
				position := len([]rune(prefix))
				matches = append(matches, match{chatMention{Type: "skill", ResourceID: skill.ID, DisplayName: label, Start: &position}, start, end})
			}
		}
	}
	// A structured mention selects an identity at its own occurrence only.
	// Apply it before bare-name ambiguity checking, not after inference fails.
	filtered := matches[:0]
	for _, item := range matches {
		covered, selected := false, false
		for _, explicit := range bound {
			if explicit.Type == "skill" && sameSkillMentionOccurrence(query, explicit, item.mention) {
				covered = true
				selected = selected || explicit.ResourceID == item.mention.ResourceID
			}
		}
		if !covered || selected {
			filtered = append(filtered, item)
		}
	}
	matches = filtered
	byPosition := map[int]match{}
	for _, item := range matches {
		if prior, ok := byPosition[item.start]; ok && prior.mention.ResourceID != item.mention.ResourceID {
			return nil, fmt.Errorf("skill name %q is ambiguous; specify its full category/name", item.mention.DisplayName)
		}
		byPosition[item.start] = item
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
	var out []chatMention
	seen := map[int]bool{}
	for _, item := range matches {
		if !seen[item.start] {
			out = append(out, item.mention)
			seen[item.start] = true
		}
	}
	return out, nil
}

// Selections belong to a conversation, not to task classification or the model's
// guesses. The preview path evaluates the same policy without persisting it.
func applyConversationSkillPolicy(ctx context.Context, db *gorm.DB, userID, convID, sessionID, query string, mentions []chatMention, resources *evolution.ChatResourceContext, enabled, persist bool) ([]string, error) {
	if resources == nil {
		return nil, nil
	}
	var selectedNames []string
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext := map[string]json.RawMessage{}
		state := conversationSkillUsage{}
		var conv orm.Conversation
		hasConversation := false
		if convID != "" {
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "ext").Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", convID, userID).Take(&conv).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			hasConversation = err == nil
			if hasConversation && len(conv.Ext) > 0 {
				if err := json.Unmarshal(conv.Ext, &ext); err != nil {
					return fmt.Errorf("decode skill usage: %w", err)
				}
				if ext == nil {
					ext = map[string]json.RawMessage{}
				}
				if raw := ext[conversationSkillUsageKey]; len(raw) > 0 {
					if err := json.Unmarshal(raw, &state); err != nil {
						return err
					}
				}
			}
		}
		if !enabled {
			return nil
		}
		var skills []orm.SkillV2Skill
		if err := tx.Where("owner_user_id = ? AND deleted_at IS NULL AND head_revision_id IS NOT NULL", userID).Order("id").Find(&skills).Error; err != nil {
			return err
		}
		inferred, err := explicitSkillMentions(query, skills, mentions...)
		if err != nil {
			return err
		}
		// Structured @ selections already disambiguate a name in the visible text.
		for _, mention := range inferred {
			covered := false
			for _, explicit := range mentions {
				if explicit.Type == "skill" && sameSkillMentionOccurrence(query, explicit, mention) {
					covered = true
					break
				}
			}
			if !covered {
				mentions = append(mentions, mention)
			}
		}
		mentions = append(mentions, globalSkillDenyMentions(query)...)
		sort.SliceStable(mentions, func(i, j int) bool {
			return skillMentionPosition(query, mentions[i]) < skillMentionPosition(query, mentions[j])
		})
		selected, denied := map[string]bool{}, map[string]bool{}
		for _, id := range state.SelectedIDs {
			selected[id] = true
		}
		for _, id := range state.ExcludedIDs {
			denied[id] = true
		}
		byID := map[string]orm.SkillV2Skill{}
		for _, skill := range skills {
			byID[skill.ID] = skill
		}
		for _, mention := range mentions {
			if mention.Type == globalSkillDenyAction {
				for id := range byID {
					delete(selected, id)
					denied[id] = true
				}
				continue
			}
			if mention.Type != "skill" {
				continue
			}
			if _, ok := byID[mention.ResourceID]; !ok {
				return fmt.Errorf("skill mention is not accessible or unpublished: %s", mention.ResourceID)
			}
			if mentionIsDenied(query, mention) {
				delete(selected, mention.ResourceID)
				denied[mention.ResourceID] = true
			} else {
				selected[mention.ResourceID] = true
				delete(denied, mention.ResourceID)
			}
		}
		next := conversationSkillUsage{SelectedIDs: []string{}, ExcludedIDs: []string{}}
		deniedNames := map[string]bool{}
		previousSelected := map[string]bool{}
		for _, id := range state.SelectedIDs {
			previousSelected[id] = true
		}
		for _, skill := range skills {
			key := skill.Category + "/" + skill.SkillName
			if denied[skill.ID] {
				next.ExcludedIDs = append(next.ExcludedIDs, skill.ID)
				deniedNames[key] = true
				resources.ExcludedSkills = append(resources.ExcludedSkills, key)
			}
			if selected[skill.ID] && !denied[skill.ID] {
				next.SelectedIDs = append(next.SelectedIDs, skill.ID)
				selectedNames = append(selectedNames, key)
			}
		}
		resources.AvailableSkills = withoutSkillNames(resources.AvailableSkills, deniedNames)
		resources.SearchableSkills = withoutSkillNames(resources.SearchableSkills, deniedNames)
		loadContentIDs := map[string]bool{}
		for _, id := range next.SelectedIDs {
			if !previousSelected[id] {
				loadContentIDs[id] = true
			}
		}
		if err := evolution.AddMentionedSkills(ctx, tx, userID, sessionID, next.SelectedIDs, resources, persist, loadContentIDs); err != nil {
			return err
		}
		selectedSet := map[string]bool{}
		for _, id := range next.SelectedIDs {
			selectedSet[id] = true
		}
		for _, item := range state.Invocations {
			if selectedSet[item.SkillID] {
				next.Invocations = append(next.Invocations, item)
			}
		}
		loadedIDs := map[string]bool{}
		for _, item := range resources.LoadedSkills {
			loadedIDs[item.SkillID] = true
			if !invocationHasSkill(next.Invocations, item.SkillID) {
				next.Invocations = append(next.Invocations, item)
			}
		}
		for _, item := range next.Invocations {
			if loadedIDs[item.SkillID] {
				continue
			}
			resources.InvokedSkills = append(resources.InvokedSkills, item)
		}
		if persist && hasConversation && !reflect.DeepEqual(state, next) && (len(next.SelectedIDs) > 0 || len(next.ExcludedIDs) > 0 || len(state.SelectedIDs) > 0 || len(state.ExcludedIDs) > 0 || len(next.Invocations) > 0 || len(state.Invocations) > 0) {
			encoded, err := json.Marshal(next)
			if err != nil {
				return err
			}
			ext[conversationSkillUsageKey] = encoded
			payload, err := json.Marshal(ext)
			if err != nil {
				return err
			}
			return tx.Model(&orm.Conversation{}).Where("id = ? AND create_user_id = ?", convID, userID).Update("ext", payload).Error
		}
		return nil
	})
	return selectedNames, err
}

func withoutSkillNames(names []string, excluded map[string]bool) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		if !excluded[name] {
			result = append(result, name)
		}
	}
	return result
}

func invocationHasSkill(items []evolution.LoadedSkill, skillID string) bool {
	for _, item := range items {
		if item.SkillID == skillID {
			return true
		}
	}
	return false
}

func appendPersistedSkillInvocations(history []map[string]any, resources *evolution.ChatResourceContext) []map[string]any {
	if resources == nil || len(resources.InvokedSkills) == 0 {
		return history
	}
	encoded, _ := json.Marshal(history)
	blob := string(encoded)
	out := history
	if out == nil {
		out = []map[string]any{}
	}
	for _, item := range resources.InvokedSkills {
		if strings.TrimSpace(item.SkillKey) == "" || strings.TrimSpace(item.Content) == "" {
			continue
		}
		if strings.Contains(blob, item.SkillKey) && strings.Contains(blob, "get_skill") {
			continue
		}
		callID := "skill-invoke-" + item.SkillID
		if callID == "skill-invoke-" {
			callID = "skill-invoke-" + item.SkillKey
		}
		call, err := json.Marshal(map[string]any{
			"id": callID, "name": "get_skill", "arguments": map[string]any{"name": item.SkillKey},
		})
		if err != nil {
			continue
		}
		result, err := json.Marshal(map[string]any{
			"id": callID, "name": "get_skill", "result": map[string]any{
				"status": "ok", "name": item.SkillKey, "revision_id": item.RevisionID, "content": item.Content,
			},
		})
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"role":    "assistant",
			"content": "<tool_call>" + string(call) + "</tool_call><tool_result>" + string(result) + "</tool_result>",
		})
		blob += item.SkillKey
	}
	return out
}

func skillMentionPosition(query string, mention chatMention) int {
	if mention.Start != nil && *mention.Start >= 0 && *mention.Start <= len([]rune(query)) {
		return *mention.Start
	}
	if mention.DisplayName != "" {
		if index := strings.Index(strings.ToLower(query), strings.ToLower(mention.DisplayName)); index >= 0 {
			return len([]rune(query[:index]))
		}
	}
	return -1
}

func sameSkillMentionOccurrence(query string, a, b chatMention) bool {
	first, second := skillMentionPosition(query, a), skillMentionPosition(query, b)
	if first < 0 || second < 0 {
		return false
	}
	if first == second {
		return true
	}
	if first > second {
		first, second = second, first
	}
	return string([]rune(query)[first:second]) == "@"
}

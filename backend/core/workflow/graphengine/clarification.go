package graphengine

import (
	"fmt"
	"slices"
	"strings"

	"lazymind/core/common/askuser"
)

type ClarificationResult struct {
	Values    map[string]any     `json:"clarification_values"`
	Missing   []string           `json:"missing_clarification_fields"`
	Questions []askuser.Question `json:"questions"`
}

func clarificationPresent(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	if list, ok := value.([]any); ok {
		return len(list) > 0
	}
	return true // false and zero are supplied values, subject to validation.
}

func canonicalClarification(field ClarificationField, value any) any {
	if items, ok := value.([]any); ok {
		result := make([]any, len(items))
		for i, item := range items {
			result[i] = canonicalClarification(field, item)
		}
		return result
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if alias, exists := field.ValueAliases[text]; exists {
		return alias
	}
	return value
}

func validateClarificationValue(field ClarificationField, value any) error {
	values := []any{value}
	if field.Type == "multiple" {
		var ok bool
		values, ok = value.([]any)
		if !ok || len(values) == 0 {
			return fmt.Errorf("%s requires at least one selection", field.ID)
		}
	}
	seen := map[string]bool{}
	for _, item := range values {
		if _, object := item.(map[string]any); object {
			return fmt.Errorf("%s requires a scalar answer", field.ID)
		}
		if _, list := item.([]any); list {
			return fmt.Errorf("%s requires scalar selections", field.ID)
		}
		text := strings.TrimSpace(fmt.Sprint(item))
		if field.Type == "multiple" && seen[text] {
			return fmt.Errorf("%s contains duplicate selections", field.ID)
		}
		seen[text] = true
		if text == "" || text == askuser.Other {
			return fmt.Errorf("%s requires a non-empty answer", field.ID)
		}
		if field.Type == "boolean" {
			allowed := []string{"true", "false", "__ask_user_yes__", "__ask_user_no__"}
			for _, alias := range field.ValueAliases {
				allowed = append(allowed, alias)
			}
			if !slices.Contains(allowed, text) {
				return fmt.Errorf("%s requires a yes/no answer", field.ID)
			}
		}
		if field.ChoicePolicy == "fixed" && (field.AllowOther == nil || !*field.AllowOther) &&
			(field.Type == "single" || field.Type == "multiple") {
			allowed := append([]string{}, field.Choices...)
			for _, alias := range field.ValueAliases {
				allowed = append(allowed, alias)
			}
			if !slices.Contains(allowed, text) {
				return fmt.Errorf("%s has an unsupported choice", field.ID)
			}
		}
		if err := askuser.ValidateText(text, field.Validation); err != nil {
			return fmt.Errorf("%s: %w", field.ID, err)
		}
	}
	return nil
}

// ResolveClarifications is pure: no DB, model, filesystem or network calls.
// Applicable fields are evaluated in declaration order (parents precede children).
func ResolveClarifications(fields []ClarificationField, supplied map[string]any) (ClarificationResult, error) {
	result := ClarificationResult{Values: map[string]any{}, Missing: []string{}, Questions: []askuser.Question{}}
	for _, field := range fields {
		if field.Attachment != nil && field.Attachment.MaterialID == field.ID {
			// An upload choice is not an imported input resource.
			continue
		}
		if condition := field.AppliesWhen; condition != nil {
			parent, exists := result.Values[condition.Field]
			if !exists || (condition.Equals != nil && fmt.Sprint(parent) != fmt.Sprint(condition.Equals)) ||
				(condition.NotEquals != nil && fmt.Sprint(parent) == fmt.Sprint(condition.NotEquals)) {
				continue
			}
		}
		value := supplied[field.ID]
		if !clarificationPresent(value) {
			value = field.Default
		}
		if clarificationPresent(value) {
			value = canonicalClarification(field, value)
			if err := validateClarificationValue(field, value); err != nil {
				return result, err
			}
			result.Values[field.ID] = value
			continue
		}
		if !field.Required {
			continue
		}
		result.Missing = append(result.Missing, field.ID)
		if len(result.Questions) >= 3 {
			continue
		}
		choices := append([]string{}, field.Choices...)
		allowOther := field.ChoicePolicy != "fixed"
		if field.AllowOther != nil {
			allowOther = *field.AllowOther
		}
		if field.Type == "boolean" {
			choices = []string{"__ask_user_yes__", "__ask_user_no__"}
		} else if field.Type == "single" || field.Type == "multiple" {
			limit := 3
			if field.Type == "multiple" {
				limit = 4
			}
			if field.ChoicePolicy != "fixed" && len(choices) > limit {
				choices = choices[:limit]
			}
			if allowOther {
				choices = append(choices, askuser.Other)
			}
		}
		question := askuser.Question{ID: field.ID, Text: field.Question, Reason: field.Reason,
			Type: field.Type, Choices: choices, ChoicePolicy: field.ChoicePolicy, AllowOther: &allowOther,
			Required: true, BooleanLabels: field.BooleanLabels, Validation: field.Validation}
		if field.Attachment != nil {
			attachment := field.Attachment
			question.Attachment = &askuser.Attachment{WhenChoice: attachment.WhenChoice, MaterialID: attachment.MaterialID,
				Accept: attachment.Accept, MaxFiles: attachment.MaxFiles, Required: attachment.Required}
		}
		result.Questions = append(result.Questions, question)
	}
	return result, nil
}

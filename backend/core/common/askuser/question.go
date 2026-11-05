// Package askuser defines the interactive question contract shared by Chat and Workflows.
package askuser

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const Other = "__ask_user_other__"

type BooleanLabels struct {
	Yes string `json:"yes" yaml:"yes"`
	No  string `json:"no" yaml:"no"`
}

type Validation struct {
	Format    string   `json:"format,omitempty" yaml:"format,omitempty"`
	Example   string   `json:"example,omitempty" yaml:"example,omitempty"`
	MinLength int      `json:"min_length,omitempty" yaml:"min_length,omitempty"`
	MaxLength int      `json:"max_length,omitempty" yaml:"max_length,omitempty"`
	Min       *float64 `json:"min,omitempty" yaml:"min,omitempty"`
	Max       *float64 `json:"max,omitempty" yaml:"max,omitempty"`
}

type Attachment struct {
	WhenChoice string   `json:"when_choice" yaml:"when_choice"`
	MaterialID string   `json:"material_id" yaml:"material_id"`
	Accept     []string `json:"accept" yaml:"accept"`
	MaxFiles   int      `json:"max_files,omitempty" yaml:"max_files,omitempty"`
	Required   bool     `json:"required,omitempty" yaml:"required,omitempty"`
}

type Question struct {
	ID            string         `json:"id,omitempty"`
	Text          string         `json:"text"`
	Reason        string         `json:"reason,omitempty"`
	Type          string         `json:"type"`
	Choices       []string       `json:"choices,omitempty"`
	ChoicePolicy  string         `json:"choice_policy,omitempty"`
	AllowOther    *bool          `json:"allow_other,omitempty"`
	Required      bool           `json:"required,omitempty"`
	BooleanLabels *BooleanLabels `json:"boolean_labels,omitempty"`
	Validation    *Validation    `json:"validation,omitempty"`
	Attachment    *Attachment    `json:"attachment,omitempty"`
}

// ValidateText also validates numeric answers entered through Other. Blank is
// handled by the owning required/optional question, not converted to zero.
func ValidateText(value string, rule *Validation) error {
	if rule == nil {
		return nil
	}
	value = strings.TrimSpace(value)
	length := utf8.RuneCountInString(value)
	if length < rule.MinLength || (rule.MaxLength > 0 && length > rule.MaxLength) {
		return fmt.Errorf("answer length is outside the allowed range")
	}
	if rule.Format == "positive_integer" || rule.Format == "integer" {
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			(rule.Format == "positive_integer" && number <= 0) ||
			(rule.Min != nil && number < *rule.Min) || (rule.Max != nil && number > *rule.Max) {
			return fmt.Errorf("answer must be an integer in the allowed range")
		}
	}
	return nil
}

func ValidateRule(rule *Validation) error {
	if rule == nil {
		return nil
	}
	if rule.Format != "" && rule.Format != "integer" && rule.Format != "positive_integer" {
		return fmt.Errorf("unsupported answer format")
	}
	if rule.MinLength < 0 || rule.MaxLength < 0 || (rule.MaxLength > 0 && rule.MinLength > rule.MaxLength) ||
		(rule.Min != nil && (math.IsNaN(*rule.Min) || math.IsInf(*rule.Min, 0))) ||
		(rule.Max != nil && (math.IsNaN(*rule.Max) || math.IsInf(*rule.Max, 0))) ||
		(rule.Min != nil && rule.Max != nil && *rule.Min > *rule.Max) {
		return fmt.Errorf("invalid answer validation range")
	}
	return nil
}

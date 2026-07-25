package currentmemory

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	RootPath       = "memory"
	AgentsPath     = "memory/agents"
	UsersPath      = "memory/users"
	SoulPath       = "memory/agents/soul.yaml"
	ProfilePath    = "memory/users/profile.yaml"
	PreferencePath = "memory/users/preference.yaml"
	ReferencesPath = "memory/users/references"

	EntryFile = "file"
	EntryDir  = "dir"
)

const DefaultSoulYAML = `identity:
  name: LazyMind
  role: 个人智能助手
  description: 面向研究、分析和复杂任务的个人智能助手
mission:
  primary_goal: 帮助用户准确、高效地思考并完成工作
  success_definition: 输出可靠、可执行且符合用户真实目标的结果
interaction:
  relationship_mode: 协作者
  default_tone: 温暖直接
  initiative_level: 主动
  challenge_level: 建设性
  decision_mode: 先建议再确认
epistemic:
  uncertainty_style: 明确说明
  verification_mode: 必要时核验
`

const DefaultProfileYAML = `identity:
  preferred_name: null
  aliases: []
  pronouns: null
locale:
  languages: []
  timezone: null
  region: null
professional:
  roles: []
  organization: null
  industry: null
  expertise_domains: []
accessibility:
  communication_needs: []
`

const DefaultPreferenceYAML = "preferences: []\n"

var (
	ErrInvalidDocument = errors.New("invalid current memory document")
	referenceNameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

type SoulIdentity struct {
	Name        string `json:"name" yaml:"name"`
	Role        string `json:"role" yaml:"role"`
	Description string `json:"description" yaml:"description"`
}

type SoulMission struct {
	PrimaryGoal       string `json:"primary_goal" yaml:"primary_goal"`
	SuccessDefinition string `json:"success_definition" yaml:"success_definition"`
}

type SoulInteraction struct {
	RelationshipMode string `json:"relationship_mode" yaml:"relationship_mode"`
	DefaultTone      string `json:"default_tone" yaml:"default_tone"`
	InitiativeLevel  string `json:"initiative_level" yaml:"initiative_level"`
	ChallengeLevel   string `json:"challenge_level" yaml:"challenge_level"`
	DecisionMode     string `json:"decision_mode" yaml:"decision_mode"`
}

type SoulEpistemic struct {
	UncertaintyStyle string `json:"uncertainty_style" yaml:"uncertainty_style"`
	VerificationMode string `json:"verification_mode" yaml:"verification_mode"`
}

type SoulDocument struct {
	Identity    SoulIdentity    `json:"identity" yaml:"identity"`
	Mission     SoulMission     `json:"mission" yaml:"mission"`
	Interaction SoulInteraction `json:"interaction" yaml:"interaction"`
	Epistemic   SoulEpistemic   `json:"epistemic" yaml:"epistemic"`
}

type ProfileIdentity struct {
	PreferredName *string  `json:"preferred_name" yaml:"preferred_name"`
	Aliases       []string `json:"aliases" yaml:"aliases"`
	Pronouns      *string  `json:"pronouns" yaml:"pronouns"`
}

type ProfileLocale struct {
	Languages []string `json:"languages" yaml:"languages"`
	Timezone  *string  `json:"timezone" yaml:"timezone"`
	Region    *string  `json:"region" yaml:"region"`
}

type ProfileProfessional struct {
	Roles            []string `json:"roles" yaml:"roles"`
	Organization     *string  `json:"organization" yaml:"organization"`
	Industry         *string  `json:"industry" yaml:"industry"`
	ExpertiseDomains []string `json:"expertise_domains" yaml:"expertise_domains"`
}

type ProfileAccessibility struct {
	CommunicationNeeds []string `json:"communication_needs" yaml:"communication_needs"`
}

type ProfileDocument struct {
	Identity      ProfileIdentity      `json:"identity" yaml:"identity"`
	Locale        ProfileLocale        `json:"locale" yaml:"locale"`
	Professional  ProfileProfessional  `json:"professional" yaml:"professional"`
	Accessibility ProfileAccessibility `json:"accessibility" yaml:"accessibility"`
}

type PreferenceItem struct {
	Name      string `json:"name" yaml:"name"`
	Summary   string `json:"summary" yaml:"summary"`
	Ref       string `json:"ref" yaml:"ref"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	UpdatedAt string `json:"updated_at" yaml:"updated_at"`
}

type PreferenceDocument struct {
	Preferences []PreferenceItem `json:"preferences" yaml:"preferences"`
}

type ReferenceSource struct {
	Kind           string `json:"kind" yaml:"kind"`
	ConversationID string `json:"conversation_id" yaml:"conversation_id"`
}

type ReferenceDocument struct {
	Name                 string          `json:"name"`
	Summary              string          `json:"summary"`
	CreatedAt            string          `json:"created_at"`
	UpdatedAt            string          `json:"updated_at"`
	Source               ReferenceSource `json:"source"`
	ApplicationScenarios string          `json:"application_scenarios"`
	PreferenceDetails    string          `json:"preference_details"`
	Reason               string          `json:"reason"`
}

func ValidateDocumentForPath(entryPath string, content []byte) error {
	normalized := strings.Trim(strings.TrimSpace(entryPath), "/")
	switch normalized {
	case SoulPath:
		_, err := decodeYAMLMapping(content, "soul")
		return err
	case ProfilePath:
		_, err := decodeYAMLMapping(content, "profile")
		return err
	case PreferencePath:
		_, err := ParsePreferences(content)
		return err
	}
	if strings.HasPrefix(normalized, ReferencesPath+"/") {
		if !IsCanonicalReferencePath(normalized) {
			return invalidDocument("invalid reference path %q", entryPath)
		}
		_, err := ParseReference(content)
		return err
	}
	return nil
}

func ParseSoul(content []byte) (SoulDocument, error) {
	root, err := decodeYAMLMapping(content, "soul")
	if err != nil {
		return SoulDocument{}, err
	}
	sections, err := exactMapping(root, "soul", []string{"identity", "mission", "interaction", "epistemic"})
	if err != nil {
		return SoulDocument{}, err
	}
	for section, fields := range map[string][]string{
		"identity":    {"name", "role", "description"},
		"mission":     {"primary_goal", "success_definition"},
		"interaction": {"relationship_mode", "default_tone", "initiative_level", "challenge_level", "decision_mode"},
		"epistemic":   {"uncertainty_style", "verification_mode"},
	} {
		values, mapErr := exactMapping(sections[section], section, fields)
		if mapErr != nil {
			return SoulDocument{}, mapErr
		}
		for _, field := range fields {
			if !isStringNode(values[field]) || strings.TrimSpace(values[field].Value) == "" {
				return SoulDocument{}, invalidDocument(
					"soul %q must be a non-empty string",
					section+"."+field,
				)
			}
		}
	}
	var document SoulDocument
	if err := root.Decode(&document); err != nil {
		return SoulDocument{}, invalidDocument("invalid soul: %v", err)
	}
	return document, nil
}

func ParseProfile(content []byte) (ProfileDocument, error) {
	root, err := decodeYAMLMapping(content, "profile")
	if err != nil {
		return ProfileDocument{}, err
	}
	sections, err := exactMapping(root, "profile", []string{"identity", "locale", "professional", "accessibility"})
	if err != nil {
		return ProfileDocument{}, err
	}
	sectionFields := map[string][]string{
		"identity":      {"preferred_name", "aliases", "pronouns"},
		"locale":        {"languages", "timezone", "region"},
		"professional":  {"roles", "organization", "industry", "expertise_domains"},
		"accessibility": {"communication_needs"},
	}
	stringFields := map[string]bool{
		"identity.preferred_name":   true,
		"identity.pronouns":         true,
		"locale.timezone":           true,
		"locale.region":             true,
		"professional.organization": true,
		"professional.industry":     true,
	}
	for section, fields := range sectionFields {
		values, mapErr := exactMapping(sections[section], section, fields)
		if mapErr != nil {
			return ProfileDocument{}, mapErr
		}
		for _, field := range fields {
			value := values[field]
			fullName := section + "." + field
			if stringFields[fullName] {
				if !isNullNode(value) && !isStringNode(value) {
					return ProfileDocument{}, invalidDocument(
						"field %q must be a string or null",
						fullName,
					)
				}
				continue
			}
			if err := validateOptionalStringList(value, fullName); err != nil {
				return ProfileDocument{}, err
			}
		}
	}
	var document ProfileDocument
	if err := root.Decode(&document); err != nil {
		return ProfileDocument{}, invalidDocument("invalid profile: %v", err)
	}
	return document, nil
}

func ParsePreferences(content []byte) (PreferenceDocument, error) {
	root, err := decodeYAMLMapping(content, "preference")
	if err != nil {
		return PreferenceDocument{}, err
	}
	fields, err := exactMapping(root, "preference", []string{"preferences"})
	if err != nil {
		return PreferenceDocument{}, err
	}
	itemsNode := fields["preferences"]
	if itemsNode.Kind != yaml.SequenceNode {
		return PreferenceDocument{}, invalidDocument("preference 'preferences' must be a list")
	}
	items := make([]PreferenceItem, 0, len(itemsNode.Content))
	seenNames := make(map[string]struct{}, len(itemsNode.Content))
	seenRefs := make(map[string]struct{}, len(itemsNode.Content))
	for index, rawItem := range itemsNode.Content {
		fieldName := fmt.Sprintf("preferences[%d]", index)
		values, mapErr := exactMapping(
			rawItem,
			fieldName,
			[]string{"name", "summary", "ref", "created_at", "updated_at"},
		)
		if mapErr != nil {
			return PreferenceDocument{}, mapErr
		}
		for _, field := range []string{"name", "summary", "ref", "created_at", "updated_at"} {
			if !isStringNode(values[field]) {
				return PreferenceDocument{}, invalidDocument(
					"field %q values must all be strings",
					fieldName,
				)
			}
		}
		item := PreferenceItem{
			Name:      strings.TrimSpace(values["name"].Value),
			Summary:   strings.TrimSpace(values["summary"].Value),
			Ref:       strings.TrimSpace(values["ref"].Value),
			CreatedAt: strings.TrimSpace(values["created_at"].Value),
			UpdatedAt: strings.TrimSpace(values["updated_at"].Value),
		}
		if err := validatePreferenceItem(item); err != nil {
			return PreferenceDocument{}, err
		}
		if _, exists := seenNames[item.Name]; exists {
			return PreferenceDocument{}, invalidDocument("duplicate preference item name %q", item.Name)
		}
		if _, exists := seenRefs[item.Ref]; exists {
			return PreferenceDocument{}, invalidDocument("duplicate preference reference %q", item.Ref)
		}
		seenNames[item.Name] = struct{}{}
		seenRefs[item.Ref] = struct{}{}
		items = append(items, item)
	}
	return PreferenceDocument{Preferences: items}, nil
}

func ParseReference(content []byte) (ReferenceDocument, error) {
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return ReferenceDocument{}, err
	}
	root, err := decodeYAMLMapping(frontmatter, "reference")
	if err != nil {
		return ReferenceDocument{}, err
	}
	fields, err := exactMapping(
		root,
		"reference",
		[]string{"name", "summary", "created_at", "updated_at", "source"},
	)
	if err != nil {
		return ReferenceDocument{}, err
	}
	for _, field := range []string{"name", "summary", "created_at", "updated_at"} {
		if !isStringNode(fields[field]) {
			return ReferenceDocument{}, invalidDocument("reference %q must be a string", field)
		}
	}
	name := strings.TrimSpace(fields["name"].Value)
	summary := strings.TrimSpace(fields["summary"].Value)
	if name == "" {
		return ReferenceDocument{}, invalidDocument("reference 'name' must be a non-empty string")
	}
	if summary == "" {
		return ReferenceDocument{}, invalidDocument("reference 'summary' must be a non-empty string")
	}
	if utf8.RuneCountInString(summary) > 100 {
		return ReferenceDocument{}, invalidDocument("reference 'summary' must be 100 characters or less")
	}
	createdAt := strings.TrimSpace(fields["created_at"].Value)
	updatedAt := strings.TrimSpace(fields["updated_at"].Value)
	if err := validateTimestampOrder(createdAt, updatedAt, "reference"); err != nil {
		return ReferenceDocument{}, err
	}
	sourceFields, err := exactMapping(fields["source"], "source", []string{"kind", "conversation_id"})
	if err != nil {
		return ReferenceDocument{}, err
	}
	for _, field := range []string{"kind", "conversation_id"} {
		if !isStringNode(sourceFields[field]) || strings.TrimSpace(sourceFields[field].Value) == "" {
			return ReferenceDocument{}, invalidDocument(
				"reference 'source.%s' must be a non-empty string",
				field,
			)
		}
	}
	sourceKind := strings.TrimSpace(sourceFields["kind"].Value)
	if sourceKind != "memory_review" && sourceKind != "chat_explicit" {
		return ReferenceDocument{}, invalidDocument(
			"reference 'source.kind' must be either 'memory_review' or 'chat_explicit'",
		)
	}
	sections := extractReferenceSections(body)
	for _, section := range []string{"Application Scenarios", "Preference Details", "Reason"} {
		if strings.TrimSpace(sections[section]) == "" {
			return ReferenceDocument{}, invalidDocument(
				"reference requires a non-empty '## %s' section",
				section,
			)
		}
	}
	return ReferenceDocument{
		Name:                 name,
		Summary:              summary,
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
		Source:               ReferenceSource{Kind: sourceKind, ConversationID: strings.TrimSpace(sourceFields["conversation_id"].Value)},
		ApplicationScenarios: sections["Application Scenarios"],
		PreferenceDetails:    sections["Preference Details"],
		Reason:               sections["Reason"],
	}, nil
}

func RenderSoul(document SoulDocument) ([]byte, error) {
	return renderValidatedYAML(SoulPath, document)
}

func RenderProfile(document ProfileDocument) ([]byte, error) {
	return renderValidatedYAML(ProfilePath, document)
}

func RenderPreferences(document PreferenceDocument) ([]byte, error) {
	if document.Preferences == nil {
		document.Preferences = []PreferenceItem{}
	}
	return renderValidatedYAML(PreferencePath, document)
}

func IsCanonicalReferencePath(entryPath string) bool {
	normalized := strings.Trim(strings.TrimSpace(entryPath), "/")
	if !strings.HasPrefix(normalized, ReferencesPath+"/") {
		return false
	}
	name := strings.TrimPrefix(normalized, ReferencesPath+"/")
	if strings.Contains(name, "/") || !strings.HasSuffix(name, ".md") {
		return false
	}
	return referenceNameRE.MatchString(strings.TrimSuffix(name, ".md"))
}

func SplitReferenceRef(ref string) (string, string, error) {
	raw := strings.TrimSpace(ref)
	if raw == "" {
		return "", "", invalidDocument("reference ref is required")
	}
	pathPart, anchor, _ := strings.Cut(raw, "#")
	pathPart = normalizeMemoryPath(pathPart)
	if strings.HasPrefix(pathPart, "references/") {
		pathPart = UsersPath + "/" + pathPart
	}
	if !IsCanonicalReferencePath(pathPart) {
		return "", "", invalidDocument("invalid reference ref %q", ref)
	}
	return pathPart, strings.TrimSpace(anchor), nil
}

func renderValidatedYAML(entryPath string, document any) ([]byte, error) {
	content, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	if err := ValidateDocumentForPath(entryPath, content); err != nil {
		return nil, err
	}
	return content, nil
}

func validatePreferenceItem(item PreferenceItem) error {
	if item.Name == "" {
		return invalidDocument("preference item name is required")
	}
	if item.Summary == "" {
		return invalidDocument("preference item %q requires a non-empty summary", item.Name)
	}
	if utf8.RuneCountInString(item.Summary) > 100 {
		return invalidDocument(
			"preference item %q summary must be 100 characters or less",
			item.Name,
		)
	}
	if _, _, err := SplitReferenceRef(item.Ref); err != nil {
		return invalidDocument("preference item %q has invalid ref: %v", item.Name, err)
	}
	return validateTimestampOrder(item.CreatedAt, item.UpdatedAt, item.Name)
}

func validateTimestampOrder(createdAt, updatedAt, field string) error {
	created, err := parseISODateTime(createdAt)
	if err != nil {
		return invalidDocument("%s.created_at must be an ISO 8601 datetime string with timezone", field)
	}
	updated, err := parseISODateTime(updatedAt)
	if err != nil {
		return invalidDocument("%s.updated_at must be an ISO 8601 datetime string with timezone", field)
	}
	if updated.Before(created) {
		return invalidDocument("%s updated_at cannot precede created_at", field)
	}
	return nil
}

func parseISODateTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-0700",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("invalid ISO datetime")
}

func decodeYAMLMapping(content []byte, label string) (*yaml.Node, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, invalidDocument("%s requires a non-empty YAML mapping", label)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, invalidDocument("%s must be a valid non-empty YAML mapping: %v", label, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, invalidDocument("%s must contain exactly one YAML document", label)
		}
		return nil, invalidDocument("%s must be valid YAML: %v", label, err)
	}
	if len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode ||
		len(document.Content[0].Content) == 0 {
		return nil, invalidDocument("%s must be a valid non-empty YAML mapping", label)
	}
	return document.Content[0], nil
}

func exactMapping(node *yaml.Node, field string, allowed []string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, invalidDocument("field %q must be a mapping", field)
	}
	values := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index]
		if !isStringNode(key) {
			return nil, invalidDocument("field %q keys must be strings", field)
		}
		if _, exists := values[key.Value]; exists {
			return nil, invalidDocument("field %q contains duplicate key %q", field, key.Value)
		}
		values[key.Value] = node.Content[index+1]
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	extra := make([]string, 0)
	for key := range values {
		if _, ok := allowedSet[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		return nil, invalidDocument(
			"field %q has unsupported keys: %s",
			field,
			strings.Join(extra, ", "),
		)
	}
	missing := make([]string, 0)
	for _, key := range allowed {
		if _, ok := values[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, invalidDocument(
			"field %q requires: %s",
			field,
			strings.Join(missing, ", "),
		)
	}
	return values, nil
}

func validateOptionalStringList(node *yaml.Node, field string) error {
	if node == nil || node.Kind != yaml.SequenceNode {
		return invalidDocument("field %q must be a list of strings", field)
	}
	for _, item := range node.Content {
		if !isStringNode(item) {
			return invalidDocument("field %q must be a list of strings", field)
		}
	}
	return nil
}

func isStringNode(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str"
}

func isNullNode(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func splitFrontmatter(content []byte) ([]byte, string, error) {
	text := string(content)
	firstNewline := strings.IndexByte(text, '\n')
	if firstNewline < 0 || strings.TrimSpace(text[:firstNewline]) != "---" {
		return nil, "", invalidDocument("reference must contain YAML frontmatter")
	}
	remainder := text[firstNewline+1:]
	offset := 0
	for {
		newline := strings.IndexByte(remainder[offset:], '\n')
		lineEnd := len(remainder)
		if newline >= 0 {
			lineEnd = offset + newline
		}
		line := strings.TrimSuffix(remainder[offset:lineEnd], "\r")
		if strings.TrimSpace(line) == "---" {
			frontmatter := []byte(remainder[:offset])
			bodyStart := lineEnd
			if newline >= 0 {
				bodyStart++
			}
			return frontmatter, remainder[bodyStart:], nil
		}
		if newline < 0 {
			break
		}
		offset = lineEnd + 1
	}
	return nil, "", invalidDocument("reference must contain YAML frontmatter")
}

func extractReferenceSections(body string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	sections := make(map[string]string)
	current := ""
	buffer := make([]string, 0)
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimSpace(strings.Join(buffer, "\n"))
		}
		buffer = buffer[:0]
	}
	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(trimmedRight, "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(trimmedRight, "## "))
			continue
		}
		if current != "" {
			buffer = append(buffer, line)
		}
	}
	flush()
	return sections
}

func normalizeMemoryPath(raw string) string {
	value := strings.TrimSpace(raw)
	if _, after, ok := strings.Cut(value, "://"); ok {
		value = after
	}
	return strings.Trim(value, "/")
}

func invalidDocument(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDocument, fmt.Sprintf(format, args...))
}

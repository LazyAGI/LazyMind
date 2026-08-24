package showcase

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "golang.org/x/image/webp"
	"gopkg.in/yaml.v3"
)

const (
	CatalogSchemaVersion = 2

	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusDisabled  = "disabled"
	TypeChat        = "chat"
	TypeWork        = "work"

	ResultTemplateGeneric = "generic_report_v1"
	ResultTemplateProduct = "product_report_v1"

	maxAssetBytes      = 5 << 20
	maxExperienceBytes = 20 << 20
)

var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9]+(?:[a-z0-9_-]*[a-z0-9])?$`)
	localePattern  = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

	allowedAssetRoles    = map[string]struct{}{"cover": {}, "result": {}}
	allowedFeaturedTypes = map[string]struct{}{TypeChat: {}, TypeWork: {}}
	allowedAssetMIMEs    = map[string]struct{}{"image/jpeg": {}, "image/png": {}, "image/webp": {}}
	assetMIMEByExtension = map[string]string{".jpeg": "image/jpeg", ".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}
	allowedOutputTypes   = map[string]struct{}{
		"dashboard": {}, "document": {}, "images": {}, "meeting": {},
		"report": {}, "slides": {}, "table": {}, "web": {},
	}
)

type Catalog struct {
	SchemaVersion int                  `json:"schema_version"`
	Cases         []FeaturedDefinition `json:"cases"`
}

type definitionError string

func (err definitionError) Error() string { return string(err) }

func definitionFailure(format string, args ...any) error {
	return definitionError(fmt.Sprintf(format, args...))
}

type FeaturedDefinition struct {
	SchemaVersion  int                       `yaml:"schema_version,omitempty" json:"-"`
	ID             string                    `yaml:"id" json:"id"`
	Type           string                    `yaml:"type" json:"type"`
	Version        string                    `yaml:"version" json:"version"`
	Status         string                    `yaml:"status" json:"status"`
	DefaultLocale  string                    `yaml:"default_locale" json:"default_locale"`
	Skill          FeaturedSkillBinding      `yaml:"skill" json:"skill"`
	Placement      FeaturedPlacement         `yaml:"placement" json:"placement"`
	Classification FeaturedClassification    `yaml:"classification" json:"classification"`
	Assets         map[string]FeaturedAsset  `yaml:"assets" json:"assets"`
	Presentation   FeaturedPresentation      `yaml:"presentation" json:"presentation"`
	Tasks          []FeaturedTask            `yaml:"tasks" json:"tasks"`
	Locales        map[string]FeaturedLocale `yaml:"-" json:"locales,omitempty"`
	sourceDir      string
}

type FeaturedSkillBinding struct {
	SourceURL       string `yaml:"source_url" json:"source_url"`
	RequiredVersion string `yaml:"required_version,omitempty" json:"required_version,omitempty"`
	BuiltinSkillUID string `yaml:"-" json:"builtin_skill_uid"`
	Version         string `yaml:"-" json:"version"`
	ArchiveSHA256   string `yaml:"-" json:"archive_sha256"`
}

type FeaturedPlacement struct {
	Home    bool `yaml:"home" json:"home"`
	Gallery bool `yaml:"gallery" json:"gallery"`
	Order   int  `yaml:"order" json:"order"`
}

type FeaturedClassification struct {
	Category string   `yaml:"category" json:"category"`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

type FeaturedAsset struct {
	File       string `yaml:"file,omitempty" json:"-"`
	Role       string `yaml:"role" json:"role"`
	URL        string `yaml:"-" json:"url"`
	SHA256     string `yaml:"-" json:"sha256"`
	MIME       string `yaml:"-" json:"mime"`
	Size       int64  `yaml:"-" json:"size"`
	Width      int    `yaml:"-" json:"width"`
	Height     int    `yaml:"-" json:"height"`
	sourcePath string
}

type FeaturedPresentation struct {
	Card   FeaturedCardPresentation   `yaml:"card" json:"card"`
	Detail FeaturedDetailPresentation `yaml:"detail" json:"detail"`
}

type FeaturedCardPresentation struct {
	Title         string `yaml:"title" json:"title"`
	Description   string `yaml:"description" json:"description"`
	OutputType    string `yaml:"output_type" json:"output_type"`
	OutputLabel   string `yaml:"output_label" json:"output_label"`
	CoverAsset    string `yaml:"cover_asset" json:"cover_asset"`
	ImageURL      string `yaml:"-" json:"image_url"`
	ResultSummary string `yaml:"result_summary" json:"result_summary"`
}

type FeaturedDetailPresentation struct {
	Title          string `yaml:"title" json:"title"`
	Description    string `yaml:"description" json:"description"`
	AttachmentHint string `yaml:"attachment_hint,omitempty" json:"attachment_hint,omitempty"`
}

type FeaturedTask struct {
	ID       string               `yaml:"id" json:"id"`
	Selector FeaturedTaskSelector `yaml:"selector" json:"selector"`
	Launch   FeaturedTaskLaunch   `yaml:"launch" json:"launch"`
	Replay   FeaturedTaskReplay   `yaml:"replay" json:"replay"`
	Result   ShowcaseCaseResult   `yaml:"result" json:"result"`
}

type FeaturedTaskSelector struct {
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description" json:"description"`
	OutputLabel string `yaml:"output_label" json:"output_label"`
}

type FeaturedTaskLaunch struct {
	PromptShort string `yaml:"prompt_short" json:"prompt_short"`
	Prompt      string `yaml:"prompt" json:"prompt"`
}

type FeaturedTaskReplay struct {
	Steps []ShowcaseCaseStep `yaml:"steps" json:"steps"`
}

type FeaturedLocale struct {
	Locale         string                 `yaml:"locale" json:"-"`
	Classification FeaturedClassification `yaml:"classification" json:"classification"`
	Presentation   FeaturedPresentation   `yaml:"presentation" json:"presentation"`
	Tasks          []FeaturedTask         `yaml:"tasks" json:"tasks"`
}

func CatalogPath() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return ""
	}
	return firstFeaturedCatalogPath(featuredCatalogPathCandidates(workingDirectory))
}

func featuredCatalogPathCandidates(workingDirectory string) []string {
	return []string{
		filepath.Join(string(filepath.Separator), "skills", ".runtime", "featured-skills", "catalog.json"),
		filepath.Join(workingDirectory, "..", "..", "skills", ".runtime", "featured-skills", "catalog.json"),
		filepath.Join(workingDirectory, "..", "..", "..", "featured-skills", "catalog.json"),
	}
}

func firstFeaturedCatalogPath(candidates []string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr == nil {
				return filepath.Clean(absolute)
			}
		}
	}
	return ""
}

func LoadSourceDirectory(root string) ([]FeaturedDefinition, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	definitions := make([]FeaturedDefinition, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		filePath := filepath.Join(directory, "featured.yaml")
		var definition FeaturedDefinition
		if err := decodeYAMLFile(filePath, &definition); err != nil {
			return nil, err
		}
		if definition.SchemaVersion != CatalogSchemaVersion {
			return nil, definitionFailure("%s has unsupported schema %d", filePath, definition.SchemaVersion)
		}
		if definition.ID != entry.Name() {
			return nil, definitionFailure("%s id %q must match directory %q", filePath, definition.ID, entry.Name())
		}
		definition.sourceDir = directory
		if err := inspectAssets(&definition); err != nil {
			return nil, definitionFailure("%s: %v", filePath, err)
		}
		locales, err := loadLocales(directory)
		if err != nil {
			return nil, err
		}
		definition.Locales = locales
		if err := validateDefinition(definition, false); err != nil {
			return nil, definitionFailure("%s: %v", filePath, err)
		}
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	return definitions, nil
}

func CompileCatalog(definitions []FeaturedDefinition, outputRoot string) (Catalog, error) {
	catalog := Catalog{SchemaVersion: CatalogSchemaVersion}
	for _, definition := range definitions {
		for assetID, asset := range definition.Assets {
			filename := asset.SHA256[:12] + "-" + filepath.Base(asset.File)
			relative := path.Join("assets", definition.ID, definition.Version, filename)
			destination := filepath.Join(outputRoot, filepath.FromSlash(relative))
			if err := copyAsset(asset.sourcePath, destination); err != nil {
				return Catalog{}, definitionFailure("copy featured asset %s/%s: %v", definition.ID, assetID, err)
			}
			asset.File = ""
			asset.sourcePath = ""
			asset.URL = "/showcase-assets/" + strings.TrimPrefix(relative, "assets/")
			definition.Assets[assetID] = asset
		}
		if err := resolveExperienceAssets(&definition); err != nil {
			return Catalog{}, err
		}
		if err := validateDefinition(definition, true); err != nil {
			return Catalog{}, definitionFailure("compiled featured Skill %s: %v", definition.ID, err)
		}
		catalog.Cases = append(catalog.Cases, definition)
	}
	return catalog, nil
}

func LoadCatalog(catalogPath string) (Catalog, error) {
	body, err := os.ReadFile(catalogPath)
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, definitionFailure("parse featured Skill catalog %s: %v", catalogPath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Catalog{}, definitionFailure("parse featured Skill catalog %s: trailing JSON content", catalogPath)
	}
	if catalog.SchemaVersion != CatalogSchemaVersion {
		return Catalog{}, definitionFailure("unsupported featured Skill catalog schema %d", catalog.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(catalog.Cases))
	for _, definition := range catalog.Cases {
		if _, exists := seen[definition.ID]; exists {
			return Catalog{}, definitionFailure("duplicate featured Skill id %q", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		if err := validateDefinition(definition, true); err != nil {
			return Catalog{}, definitionFailure("featured Skill %s: %v", definition.ID, err)
		}
	}
	return catalog, nil
}

func (c Catalog) ShowcaseCases(locale string) []ShowcaseCase {
	cases := make([]ShowcaseCase, 0, len(c.Cases))
	for _, definition := range c.Cases {
		if definition.Status != StatusPublished || !definition.Placement.Home && !definition.Placement.Gallery {
			continue
		}
		presentation := definition.Presentation
		tasks := definition.Tasks
		classification := definition.Classification
		if translated, ok := definition.Locales[locale]; ok {
			classification = translated.Classification
			presentation = translated.Presentation
			tasks = translated.Tasks
		}
		caseTasks := make([]ShowcaseCaseTask, 0, len(tasks))
		for _, task := range tasks {
			caseTasks = append(caseTasks, ShowcaseCaseTask{
				ID:          task.ID,
				Title:       task.Selector.Title,
				Description: task.Selector.Description,
				OutputLabel: task.Selector.OutputLabel,
				PromptShort: task.Launch.PromptShort,
				Prompt:      task.Launch.Prompt,
				Steps:       append([]ShowcaseCaseStep(nil), task.Replay.Steps...),
				Result:      task.Result,
			})
		}
		firstTask := caseTasks[0]
		cases = append(cases, ShowcaseCase{
			ID:                definition.ID,
			Type:              definition.Type,
			Title:             presentation.Card.Title,
			Description:       presentation.Card.Description,
			DetailTitle:       presentation.Detail.Title,
			DetailDescription: presentation.Detail.Description,
			Category:          classification.Category,
			Tags:              append([]string(nil), classification.Tags...),
			OutputType:        presentation.Card.OutputType,
			OutputLabel:       presentation.Card.OutputLabel,
			ImageURL:          presentation.Card.ImageURL,
			AttachmentHint:    presentation.Detail.AttachmentHint,
			PromptShort:       firstTask.PromptShort,
			Prompt:            firstTask.Prompt,
			ResultSummary:     firstTask.Result.Summary,
			ResultHighlights:  append([]string(nil), firstTask.Result.Highlights...),
			Steps:             append([]ShowcaseCaseStep(nil), firstTask.Steps...),
			Tasks:             caseTasks,
			BuiltinSkillUID:   definition.Skill.BuiltinSkillUID,
			Featured:          definition.Placement.Home,
			FeaturedOrder:     definition.Placement.Order,
			Gallery:           definition.Placement.Gallery,
			SearchAliases:     defaultSearchAliases(definition),
		})
	}
	sort.SliceStable(cases, func(i, j int) bool {
		return cases[i].FeaturedOrder < cases[j].FeaturedOrder
	})
	return cases
}

func decodeYAMLFile(filePath string, target any) error {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return definitionFailure("read %s: %v", filePath, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return definitionFailure("parse %s: %v", filePath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return definitionFailure("parse %s: multiple YAML documents are not supported", filePath)
		}
		return definitionFailure("parse %s: %v", filePath, err)
	}
	return nil
}

func loadLocales(directory string) (map[string]FeaturedLocale, error) {
	localesDir := filepath.Join(directory, "locales")
	entries, err := os.ReadDir(localesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	locales := make(map[string]FeaturedLocale, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if filepath.Ext(entry.Name()) != ".yaml" {
			return nil, definitionFailure("unsupported locale file %s", filepath.Join(localesDir, entry.Name()))
		}
		filePath := filepath.Join(localesDir, entry.Name())
		var locale FeaturedLocale
		if err := decodeYAMLFile(filePath, &locale); err != nil {
			return nil, err
		}
		if strings.TrimSuffix(entry.Name(), ".yaml") != locale.Locale {
			return nil, definitionFailure("%s locale %q must match filename", filePath, locale.Locale)
		}
		if _, exists := locales[locale.Locale]; exists {
			return nil, definitionFailure("duplicate featured locale %q", locale.Locale)
		}
		locales[locale.Locale] = locale
	}
	return locales, nil
}

func inspectAssets(definition *FeaturedDefinition) error {
	var total int64
	for assetID, asset := range definition.Assets {
		if !slugPattern.MatchString(assetID) {
			return definitionFailure("invalid asset id %q", assetID)
		}
		if _, ok := allowedAssetRoles[asset.Role]; !ok {
			return definitionFailure("asset %s has invalid role %q", assetID, asset.Role)
		}
		relative, err := safeAssetPath(asset.File)
		if err != nil {
			return definitionFailure("asset %s: %v", assetID, err)
		}
		sourcePath := filepath.Join(definition.sourceDir, filepath.FromSlash(relative))
		if err := rejectSymlinkPath(definition.sourceDir, relative); err != nil {
			return definitionFailure("asset %s: %v", assetID, err)
		}
		body, err := os.ReadFile(sourcePath)
		if err != nil {
			return definitionFailure("asset %s: %v", assetID, err)
		}
		if len(body) == 0 || len(body) > maxAssetBytes {
			return definitionFailure("asset %s size must be between 1 and %d bytes", assetID, maxAssetBytes)
		}
		total += int64(len(body))
		if total > maxExperienceBytes {
			return definitionFailure("featured assets exceed %d bytes", maxExperienceBytes)
		}
		mimeType := http.DetectContentType(body[:min(len(body), 512)])
		if _, ok := allowedAssetMIMEs[mimeType]; !ok {
			return definitionFailure("asset %s has unsupported MIME %q", assetID, mimeType)
		}
		if assetMIMEByExtension[strings.ToLower(filepath.Ext(relative))] != mimeType {
			return definitionFailure("asset %s file extension does not match MIME %q", assetID, mimeType)
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || config.Width < 64 || config.Height < 64 || config.Width > 8192 || config.Height > 8192 {
			return definitionFailure("asset %s has invalid image dimensions", assetID)
		}
		hash := sha256.Sum256(body)
		asset.File = relative
		asset.sourcePath = sourcePath
		asset.SHA256 = hex.EncodeToString(hash[:])
		asset.MIME = mimeType
		asset.Size = int64(len(body))
		asset.Width = config.Width
		asset.Height = config.Height
		definition.Assets[assetID] = asset
	}
	return nil
}

func safeAssetPath(value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || strings.ContainsRune(value, 0) {
		return "", definitionFailure("unsafe asset file %q", value)
	}
	cleaned := path.Clean(value)
	if cleaned != value || !strings.HasPrefix(cleaned, "assets/") || cleaned == "assets" {
		return "", definitionFailure("asset file must be a normalized path under assets/")
	}
	return cleaned, nil
}

func rejectSymlinkPath(root, relative string) error {
	current := root
	for _, segment := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return definitionFailure("symlink is not allowed: %s", relative)
		}
	}
	return nil
}

func resolveExperienceAssets(definition *FeaturedDefinition) error {
	resolve := func(presentation *FeaturedPresentation, tasks []FeaturedTask) ([]FeaturedTask, error) {
		cover, ok := definition.Assets[presentation.Card.CoverAsset]
		if !ok {
			return nil, definitionFailure("featured Skill %s references missing cover asset %q", definition.ID, presentation.Card.CoverAsset)
		}
		presentation.Card.ImageURL = cover.URL
		for index := range tasks {
			assetID := tasks[index].Result.ImageAsset
			if assetID == "" {
				continue
			}
			asset, ok := definition.Assets[assetID]
			if !ok {
				return nil, definitionFailure("featured Skill %s task %s references missing result asset %q", definition.ID, tasks[index].ID, assetID)
			}
			tasks[index].Result.ImageURL = asset.URL
		}
		return tasks, nil
	}
	var err error
	definition.Tasks, err = resolve(&definition.Presentation, definition.Tasks)
	if err != nil {
		return err
	}
	for locale, content := range definition.Locales {
		content.Tasks, err = resolve(&content.Presentation, content.Tasks)
		if err != nil {
			return err
		}
		definition.Locales[locale] = content
	}
	return nil
}

func validateDefinition(definition FeaturedDefinition, compiled bool) error {
	if !slugPattern.MatchString(definition.ID) || !versionPattern.MatchString(definition.Version) || !localePattern.MatchString(definition.DefaultLocale) {
		return definitionFailure("id, semantic version, and default_locale are required and must use canonical formats")
	}
	if definition.Status != StatusDraft && definition.Status != StatusPublished && definition.Status != StatusDisabled {
		return definitionFailure("invalid status %q", definition.Status)
	}
	if _, ok := allowedFeaturedTypes[definition.Type]; !ok {
		return definitionFailure("type must be chat or work")
	}
	if definition.Status == StatusPublished && !definition.Placement.Home && !definition.Placement.Gallery {
		return definitionFailure("published definition must have a placement")
	}
	if (definition.Placement.Home || definition.Placement.Gallery) && definition.Placement.Order <= 0 {
		return definitionFailure("placement.order must be positive")
	}
	parsedSource, err := url.Parse(strings.TrimSpace(definition.Skill.SourceURL))
	if err != nil || parsedSource.Host == "" || parsedSource.Scheme != "https" && parsedSource.Scheme != "http" {
		return definitionFailure("skill.source_url must be an HTTP(S) URL")
	}
	if compiled {
		if definition.Skill.BuiltinSkillUID == "" || definition.Skill.Version == "" || len(definition.Skill.ArchiveSHA256) != sha256.Size*2 {
			return definitionFailure("compiled skill binding is incomplete")
		}
		if _, err := hex.DecodeString(definition.Skill.ArchiveSHA256); err != nil {
			return definitionFailure("invalid skill archive_sha256: %v", err)
		}
	}
	if strings.TrimSpace(definition.Classification.Category) == "" {
		return definitionFailure("classification.category is required")
	}
	if len(definition.Assets) == 0 {
		return definitionFailure("at least one asset is required")
	}
	var compiledAssetBytes int64
	for assetID, asset := range definition.Assets {
		if !slugPattern.MatchString(assetID) {
			return definitionFailure("invalid asset id %q", assetID)
		}
		if _, ok := allowedAssetRoles[asset.Role]; !ok {
			return definitionFailure("asset %s has invalid role %q", assetID, asset.Role)
		}
		if compiled {
			expectedURLPrefix := "/showcase-assets/" + definition.ID + "/" + definition.Version + "/"
			if asset.File != "" || !strings.HasPrefix(asset.URL, expectedURLPrefix) || path.Clean(asset.URL) != asset.URL || strings.Contains(asset.URL, `\`) || len(asset.SHA256) != sha256.Size*2 || asset.Size <= 0 || asset.Size > maxAssetBytes || asset.Width <= 0 || asset.Height <= 0 {
				return definitionFailure("compiled asset %s is incomplete", assetID)
			}
			if _, err := hex.DecodeString(asset.SHA256); err != nil {
				return definitionFailure("compiled asset %s has invalid sha256", assetID)
			}
			if _, ok := allowedAssetMIMEs[asset.MIME]; !ok {
				return definitionFailure("compiled asset %s has invalid MIME %q", assetID, asset.MIME)
			}
			if assetMIMEByExtension[strings.ToLower(path.Ext(asset.URL))] != asset.MIME {
				return definitionFailure("compiled asset %s URL extension does not match MIME %q", assetID, asset.MIME)
			}
			compiledAssetBytes += asset.Size
			if compiledAssetBytes > maxExperienceBytes {
				return definitionFailure("compiled featured assets exceed %d bytes", maxExperienceBytes)
			}
		} else if asset.File == "" || asset.sourcePath == "" {
			return definitionFailure("source asset %s is incomplete", assetID)
		}
	}
	if err := validateExperience(definition.Presentation, definition.Tasks, definition.Assets, compiled); err != nil {
		return definitionFailure("default locale %s: %v", definition.DefaultLocale, err)
	}
	for locale, content := range definition.Locales {
		if locale == definition.DefaultLocale || !localePattern.MatchString(locale) || !compiled && content.Locale != locale {
			return definitionFailure("invalid translation locale %q", locale)
		}
		if err := validateExperience(content.Presentation, content.Tasks, definition.Assets, compiled); err != nil {
			return definitionFailure("translation %s: %v", locale, err)
		}
		if strings.TrimSpace(content.Classification.Category) == "" {
			return definitionFailure("translation %s: classification.category is required", locale)
		}
		if err := validateTaskShape(definition.Presentation, definition.Tasks, content.Presentation, content.Tasks); err != nil {
			return definitionFailure("translation %s: %v", locale, err)
		}
	}
	if err := validateAssetUsage(definition); err != nil {
		return err
	}
	return nil
}

func validateAssetUsage(definition FeaturedDefinition) error {
	used := map[string]struct{}{definition.Presentation.Card.CoverAsset: {}}
	for _, task := range definition.Tasks {
		if task.Result.ImageAsset != "" {
			used[task.Result.ImageAsset] = struct{}{}
		}
	}
	for _, content := range definition.Locales {
		used[content.Presentation.Card.CoverAsset] = struct{}{}
		for _, task := range content.Tasks {
			if task.Result.ImageAsset != "" {
				used[task.Result.ImageAsset] = struct{}{}
			}
		}
	}
	for assetID := range definition.Assets {
		if _, ok := used[assetID]; !ok {
			return definitionFailure("asset %s is not referenced by any presentation or result", assetID)
		}
	}
	return nil
}

func validateExperience(presentation FeaturedPresentation, tasks []FeaturedTask, assets map[string]FeaturedAsset, compiled bool) error {
	card := presentation.Card
	if card.Title == "" || card.Description == "" || card.OutputLabel == "" || card.ResultSummary == "" {
		return definitionFailure("presentation.card text fields are required")
	}
	if _, ok := allowedOutputTypes[card.OutputType]; !ok {
		return definitionFailure("unsupported output_type %q", card.OutputType)
	}
	if asset, ok := assets[card.CoverAsset]; !ok || asset.Role != "cover" {
		return definitionFailure("cover_asset %q must reference a cover asset", card.CoverAsset)
	}
	if compiled && card.ImageURL != assets[card.CoverAsset].URL {
		return definitionFailure("compiled cover URL does not match asset registry")
	}
	if presentation.Detail.Title == "" || presentation.Detail.Description == "" {
		return definitionFailure("presentation.detail title and description are required")
	}
	if len(tasks) == 0 {
		return definitionFailure("at least one task is required")
	}
	seen := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if !slugPattern.MatchString(task.ID) {
			return definitionFailure("task %d has invalid id %q", index, task.ID)
		}
		if _, exists := seen[task.ID]; exists {
			return definitionFailure("duplicate task id %q", task.ID)
		}
		seen[task.ID] = struct{}{}
		if task.Selector.Title == "" || task.Selector.Description == "" || task.Selector.OutputLabel == "" || task.Launch.PromptShort == "" || task.Launch.Prompt == "" {
			return definitionFailure("task %s selector and launch fields are required", task.ID)
		}
		if len(task.Replay.Steps) == 0 {
			return definitionFailure("task %s must contain at least one replay step", task.ID)
		}
		for _, step := range task.Replay.Steps {
			if step.Title == "" || step.Description == "" {
				return definitionFailure("task %s contains an incomplete replay step", task.ID)
			}
		}
		if err := validateResult(task.ID, task.Result, assets, compiled); err != nil {
			return err
		}
	}
	return nil
}

func validateResult(taskID string, result ShowcaseCaseResult, assets map[string]FeaturedAsset, compiled bool) error {
	if result.Template != ResultTemplateGeneric && result.Template != ResultTemplateProduct {
		return definitionFailure("task %s has unsupported result template %q", taskID, result.Template)
	}
	if result.Eyebrow == "" || result.Title == "" || result.Summary == "" {
		return definitionFailure("task %s result text fields are required", taskID)
	}
	if result.ImageAsset != "" {
		asset, ok := assets[result.ImageAsset]
		if !ok || asset.Role != "result" {
			return definitionFailure("task %s image_asset %q must reference a result asset", taskID, result.ImageAsset)
		}
		if compiled && result.ImageURL != asset.URL {
			return definitionFailure("task %s compiled result image URL does not match asset registry", taskID)
		}
	}
	if result.Template == ResultTemplateGeneric {
		if len(result.Highlights) == 0 || result.ProductReport != nil {
			return definitionFailure("task %s generic result requires highlights and no product_report", taskID)
		}
		return nil
	}
	if result.ProductReport == nil || len(result.ProductReport.Metrics) == 0 || len(result.ProductReport.Sections) == 0 || result.ProductReport.Deliverables == "" {
		return definitionFailure("task %s product result is incomplete", taskID)
	}
	for _, metric := range result.ProductReport.Metrics {
		if metric.Label == "" || metric.Value == "" || metric.Hint == "" {
			return definitionFailure("task %s product result contains an incomplete metric", taskID)
		}
	}
	for _, section := range result.ProductReport.Sections {
		if section.Title == "" || section.Marker != "number" && section.Marker != "letter" || len(section.Items) == 0 {
			return definitionFailure("task %s product result contains an invalid section", taskID)
		}
		for _, item := range section.Items {
			if item.Description == "" {
				return definitionFailure("task %s product result contains an incomplete section item", taskID)
			}
		}
	}
	return nil
}

func validateTaskShape(defaultPresentation FeaturedPresentation, defaults []FeaturedTask, translatedPresentation FeaturedPresentation, translated []FeaturedTask) error {
	if defaultPresentation.Card.OutputType != translatedPresentation.Card.OutputType {
		return definitionFailure("output_type must match the default locale")
	}
	if len(defaults) != len(translated) {
		return definitionFailure("task ids must match the default locale")
	}
	for index := range defaults {
		if defaults[index].ID != translated[index].ID || defaults[index].Result.Template != translated[index].Result.Template {
			return definitionFailure("task ids and result templates must match the default locale")
		}
	}
	return nil
}

func defaultSearchAliases(definition FeaturedDefinition) []string {
	values := []string{
		definition.Presentation.Card.Title,
		definition.Presentation.Card.Description,
		definition.Presentation.Detail.Title,
		definition.Presentation.Detail.Description,
		definition.Classification.Category,
	}
	for _, task := range definition.Tasks {
		values = append(values, task.Selector.Title, task.Selector.Description, task.Launch.PromptShort, task.Result.Title, task.Result.Summary)
		values = append(values, task.Result.Highlights...)
	}
	return values
}

func copyAsset(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, body, 0o644)
}

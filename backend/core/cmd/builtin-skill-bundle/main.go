package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"lazymind/core/showcase"
	skillbuiltin "lazymind/core/skillv2/builtin"
	skillmetadata "lazymind/core/skillv2/metadata"
	skillpackage "lazymind/core/skillv2/skillpackage"
	skillpatch "lazymind/core/skillv2/skillpatch"
	"lazymind/core/workflow/graphengine"
)

const maxArchiveBytes = 64 << 20

type options struct {
	Sources         string
	Lock            string
	Cache           string
	Output          string
	FeaturedSources string
	FeaturedOutput  string
	FrozenLockfile  bool
	CheckFeatured   bool
}

type sourceList struct {
	SchemaVersion int             `yaml:"schema_version"`
	PatchCatalog  string          `yaml:"patch_catalog,omitempty"`
	Skills        []remoteSource  `yaml:"skills"`
	BundledSkills []bundledSource `yaml:"bundled_skills,omitempty"`
}

type remoteSource struct {
	SourceURL string `yaml:"source_url"`
	Category  string `yaml:"category,omitempty"`
	Provider  string `yaml:"provider,omitempty"`
}

func (source *remoteSource) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		source.SourceURL = node.Value
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return bundleFailure("skill source must be a URL string or mapping")
	}
	for index := 0; index < len(node.Content); index += 2 {
		switch node.Content[index].Value {
		case "source_url", "category", "provider":
		default:
			return bundleFailure("skill source field %s is not supported", node.Content[index].Value)
		}
	}
	type rawRemoteSource remoteSource
	return node.Decode((*rawRemoteSource)(source))
}

type bundledSource struct {
	UID      string `yaml:"uid"`
	Path     string `yaml:"path"`
	Category string `yaml:"category"`
	Version  string `yaml:"version"`
	Provider string `yaml:"provider,omitempty"`
}

type sourceSpec struct {
	SourceURL       string
	ResolvedURL     string
	Identity        string
	Key             string
	FallbackName    string
	UID             string
	Category        string
	Version         string
	FallbackVersion string
	Provider        string
	LocalPath       string
}

type sourceInput struct {
	URL             string
	Bundled         *bundledSource
	MarketVisible   bool
	FallbackName    string
	Category        string
	Provider        string
	RequiredVersion string
}

type packageMeta struct {
	Version string `json:"version"`
}

type bundleError string

func (err bundleError) Error() string { return string(err) }

func bundleFailure(format string, args ...any) error {
	return bundleError(fmt.Sprintf(format, args...))
}

func main() {
	var opts options
	flag.StringVar(&opts.Sources, "sources", "", "path to builtin skill source YAML")
	flag.StringVar(&opts.Lock, "lock", "", "path to generated builtin skill lock JSON")
	flag.StringVar(&opts.Cache, "cache", "", "download cache directory")
	flag.StringVar(&opts.Output, "output", "", "runtime output directory")
	flag.StringVar(&opts.FeaturedSources, "featured-sources", "", "directory containing featured Skill definitions")
	flag.StringVar(&opts.FeaturedOutput, "featured-output", "", "runtime featured Skill catalog directory")
	flag.BoolVar(&opts.FrozenLockfile, "frozen-lockfile", false, "require sources and downloaded archives to match the lock")
	flag.BoolVar(&opts.CheckFeatured, "check-featured", false, "validate featured definitions and assets without downloading Skills")
	flag.Parse()
	if err := run(context.Background(), opts, httpClient()); err != nil {
		fmt.Fprintln(os.Stderr, "builtin skill bundle:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, client *http.Client) error {
	if opts.CheckFeatured {
		if opts.FeaturedSources == "" {
			return bundleFailure("featured-sources is required with check-featured")
		}
		definitions, err := showcase.LoadSourceDirectory(opts.FeaturedSources)
		if err != nil {
			return err
		}
		if err := validateFeaturedWorkflowBindings(definitions, opts.FeaturedSources); err != nil {
			return err
		}
		fmt.Printf("Validated %d featured capabilities\n", len(definitions))
		return nil
	}
	if opts.Sources == "" || opts.Lock == "" || opts.Cache == "" || opts.Output == "" {
		return bundleFailure("sources, lock, cache, and output are required")
	}
	featuredEnabled := opts.FeaturedSources != "" || opts.FeaturedOutput != ""
	if featuredEnabled && (opts.FeaturedSources == "" || opts.FeaturedOutput == "") {
		return bundleFailure("featured-sources and featured-output must be provided together")
	}
	ordinarySources, err := loadSources(opts.Sources)
	if err != nil {
		return err
	}
	var patchCatalog skillpatch.Catalog
	if ordinarySources.PatchCatalog != "" {
		patchCatalogPath := filepath.Join(filepath.Dir(opts.Sources), filepath.FromSlash(ordinarySources.PatchCatalog))
		patchCatalog, err = skillpatch.LoadCatalog(patchCatalogPath)
		if err != nil {
			return bundleFailure("load Skill patches: %v", err)
		}
	}
	sources := make([]sourceInput, 0, len(ordinarySources.Skills)+len(ordinarySources.BundledSkills))
	seenSources := make(map[string]struct{}, len(ordinarySources.Skills)+len(ordinarySources.BundledSkills))
	for index := range ordinarySources.BundledSkills {
		source := &ordinarySources.BundledSkills[index]
		sources = append(sources, sourceInput{Bundled: source, MarketVisible: true})
		seenSources[bundledSourceURL(source.Path)] = struct{}{}
	}
	for _, source := range ordinarySources.Skills {
		sources = append(sources, sourceInput{URL: source.SourceURL, MarketVisible: true, Category: source.Category, Provider: source.Provider})
		seenSources[source.SourceURL] = struct{}{}
	}
	var featuredDefinitions []showcase.FeaturedDefinition
	featuredCount := 0
	if featuredEnabled {
		featuredDefinitions, err = showcase.LoadSourceDirectory(opts.FeaturedSources)
		if err != nil {
			return err
		}
		if err := validateFeaturedWorkflowBindings(featuredDefinitions, opts.FeaturedSources); err != nil {
			return err
		}
		for index := range featuredDefinitions {
			definition := &featuredDefinitions[index]
			if definition.Status != showcase.StatusPublished {
				continue
			}
			if definition.Type == showcase.TypeWorkflow {
				continue
			}
			input, source, err := featuredSourceInput(definition.Skill.SourceURL, definition.Skill.RequiredVersion, definition.ID, definition.Skill.Category)
			if err != nil {
				return bundleFailure("featured Skill %s: %v", definition.ID, err)
			}
			if _, exists := seenSources[source]; exists {
				return bundleFailure("source %s cannot be both a market Skill and a featured-only Skill", source)
			}
			seenSources[source] = struct{}{}
			definition.Skill.SourceURL = source
			sources = append(sources, input)
		}
	}
	var lockedBySource map[string]skillbuiltin.CatalogSkill
	if opts.FrozenLockfile {
		locked, err := skillbuiltin.LoadCatalog(opts.Lock)
		if err != nil {
			return bundleFailure("load frozen lock: %v", err)
		}
		if len(locked.Skills) != len(sources) {
			return bundleFailure("frozen lock contains %d skills, sources contain %d", len(locked.Skills), len(sources))
		}
		lockedBySource = make(map[string]skillbuiltin.CatalogSkill, len(locked.Skills))
		for _, entry := range locked.Skills {
			lockedBySource[entry.SourceURL] = entry
		}
	}
	if err := os.MkdirAll(opts.Cache, 0o755); err != nil {
		return err
	}
	if err := prepareOutput(opts.Output, "builtin", "packages"); err != nil {
		return err
	}
	if featuredEnabled {
		if err := prepareOutput(opts.FeaturedOutput, "featured", "assets"); err != nil {
			return err
		}
	}

	catalog := skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion}
	seenUIDs := make(map[string]struct{}, len(sources))
	entriesBySource := make(map[string]skillbuiltin.CatalogSkill, len(sources))
	appliedPatchCounts := make(map[string]int)
	for _, source := range sources {
		spec, err := resolveSourceInput(source, filepath.Dir(opts.Sources))
		if err != nil {
			return err
		}
		var entry skillbuiltin.CatalogSkill
		var archivePath string
		var appliedPatches []skillpatch.AppliedPatch
		if opts.FrozenLockfile {
			locked, ok := lockedBySource[spec.SourceURL]
			if !ok {
				return bundleFailure("source %s is missing from frozen lock", spec.SourceURL)
			}
			if locked.ResolvedURL != spec.ResolvedURL {
				return bundleFailure("resolved URL changed for %s", spec.SourceURL)
			}
			if skillbuiltin.CatalogSkillMarketVisible(locked) != source.MarketVisible {
				return bundleFailure("distribution changed for %s", spec.SourceURL)
			}
			entry, archivePath, appliedPatches, err = materializeFrozen(ctx, client, spec, locked, opts.Cache, patchCatalog)
		} else {
			entry, archivePath, appliedPatches, err = resolveEntry(ctx, client, spec, opts.Cache, patchCatalog)
		}
		if err != nil {
			return bundleFailure("%s: %v", spec.SourceURL, err)
		}
		marketVisible := source.MarketVisible
		entry.MarketVisible = &marketVisible
		if _, exists := seenUIDs[entry.UID]; exists {
			return bundleFailure("duplicate builtin skill uid %s", entry.UID)
		}
		seenUIDs[entry.UID] = struct{}{}
		for _, patch := range appliedPatches {
			appliedPatchCounts[patch.ID]++
		}
		entry.PackageFile = filepath.ToSlash(filepath.Join("packages", entry.UID+".zip"))
		destination := filepath.Join(opts.Output, filepath.FromSlash(entry.PackageFile))
		if err := copyFile(archivePath, destination); err != nil {
			return err
		}
		catalog.Skills = append(catalog.Skills, entry)
		entriesBySource[entry.SourceURL] = entry
	}
	if err := patchCatalog.ValidateApplied(appliedPatchCounts); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(opts.Output, "catalog.json"), catalog); err != nil {
		return err
	}
	if !opts.FrozenLockfile {
		lockCatalog := catalog
		lockCatalog.Skills = append([]skillbuiltin.CatalogSkill(nil), catalog.Skills...)
		for i := range lockCatalog.Skills {
			lockCatalog.Skills[i].Content = ""
		}
		if err := writeJSONAtomic(opts.Lock, lockCatalog); err != nil {
			return err
		}
	}
	if featuredEnabled {
		compiledDefinitions := make([]showcase.FeaturedDefinition, 0, len(featuredDefinitions))
		for _, definition := range featuredDefinitions {
			if definition.Status != showcase.StatusPublished {
				continue
			}
			if definition.Type == showcase.TypeWorkflow {
				compiledDefinitions = append(compiledDefinitions, definition)
				continue
			}
			entry, ok := entriesBySource[definition.Skill.SourceURL]
			if !ok {
				return bundleFailure("featured Skill %s source was not bundled", definition.ID)
			}
			if required := strings.TrimSpace(definition.Skill.RequiredVersion); required != "" && required != entry.Version {
				return bundleFailure("featured Skill %s requires version %s, got %s", definition.ID, required, entry.Version)
			}
			definition.Skill.BuiltinSkillUID = entry.UID
			definition.Skill.Version = entry.Version
			definition.Skill.ArchiveSHA256 = entry.ArchiveSHA256
			compiledDefinitions = append(compiledDefinitions, definition)
		}
		featuredCatalog, err := showcase.CompileCatalog(compiledDefinitions, opts.FeaturedOutput)
		if err != nil {
			return err
		}
		if err := writeJSONAtomic(filepath.Join(opts.FeaturedOutput, "catalog.json"), featuredCatalog); err != nil {
			return err
		}
		featuredCount = len(compiledDefinitions)
	}
	fmt.Printf("Bundled %d builtin Skills and %d featured capabilities\n", len(catalog.Skills), featuredCount)
	return nil
}

func validateFeaturedWorkflowBindings(definitions []showcase.FeaturedDefinition, featuredSources string) error {
	workflowRoot := filepath.Join(filepath.Dir(filepath.Dir(filepath.Clean(featuredSources))), "workflows")
	for _, definition := range definitions {
		if definition.Type != showcase.TypeWorkflow {
			continue
		}
		workflowID := strings.TrimPrefix(strings.TrimSpace(definition.Workflow.WorkflowRef), "builtin:")
		packageRoot := filepath.Join(workflowRoot, workflowID)
		workflowYAML, err := os.ReadFile(filepath.Join(packageRoot, "workflow.yaml"))
		if err != nil {
			return bundleFailure("featured Workflow %s: read workflow.yaml: %v", definition.ID, err)
		}
		stateYAML, err := os.ReadFile(filepath.Join(packageRoot, "scenario", "state.yml"))
		if err != nil {
			return bundleFailure("featured Workflow %s: read scenario/state.yml: %v", definition.ID, err)
		}
		var metadata struct {
			ID string `yaml:"id"`
		}
		if err := yaml.Unmarshal(workflowYAML, &metadata); err != nil || strings.TrimSpace(metadata.ID) != workflowID {
			return bundleFailure("featured Workflow %s: workflow id must match %s", definition.ID, workflowID)
		}
		scenario, err := os.ReadFile(filepath.Join(packageRoot, "scenario", "scenario.md"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return bundleFailure("featured Workflow %s: read scenario/scenario.md: %v", definition.ID, err)
		}
		compiled := graphengine.Compile(string(workflowYAML), string(stateYAML), string(scenario), graphengine.ProfilePublish)
		if !compiled.Valid || compiled.Graph == nil {
			return bundleFailure("featured Workflow %s: invalid Workflow package: %v", definition.ID, compiled.Diagnostics)
		}
	}
	return nil
}

func loadSources(path string) (sourceList, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return sourceList{}, err
	}
	var raw sourceList
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return sourceList{}, bundleFailure("parse %s: %v", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return sourceList{}, bundleFailure("parse %s: multiple YAML documents are not supported", path)
	}
	if raw.SchemaVersion != 1 {
		return sourceList{}, bundleFailure("unsupported source schema %d", raw.SchemaVersion)
	}
	raw.PatchCatalog = filepath.ToSlash(strings.TrimSpace(raw.PatchCatalog))
	if raw.PatchCatalog != "" {
		cleaned, err := skillpackage.CleanPath(raw.PatchCatalog)
		if err != nil {
			return sourceList{}, bundleFailure("invalid patch_catalog: %v", err)
		}
		raw.PatchCatalog = cleaned
	}
	seenSources := make(map[string]struct{}, len(raw.Skills)+len(raw.BundledSkills))
	for index := range raw.Skills {
		entry := &raw.Skills[index]
		source := strings.TrimSpace(entry.SourceURL)
		if source == "" {
			return sourceList{}, bundleFailure("source URL cannot be empty")
		}
		if _, exists := seenSources[source]; exists {
			return sourceList{}, bundleFailure("duplicate source URL %s", source)
		}
		seenSources[source] = struct{}{}
		provider, err := skillbuiltin.NormalizeProvider(entry.Provider)
		if err != nil {
			return sourceList{}, bundleFailure("source %s: %v", source, err)
		}
		entry.SourceURL = source
		entry.Category = strings.TrimSpace(entry.Category)
		entry.Provider = provider
	}
	seenUIDs := make(map[string]struct{}, len(raw.BundledSkills))
	for index := range raw.BundledSkills {
		source := &raw.BundledSkills[index]
		source.UID = strings.TrimSpace(source.UID)
		source.Path = filepath.ToSlash(strings.TrimSpace(source.Path))
		source.Category = strings.TrimSpace(source.Category)
		source.Version = strings.TrimSpace(source.Version)
		provider, err := skillbuiltin.NormalizeProvider(source.Provider)
		if err != nil {
			return sourceList{}, bundleFailure("bundled skill %d: %v", index, err)
		}
		source.Provider = provider
		cleanedPath, err := skillpackage.CleanPath(source.Path)
		if err != nil || source.UID == "" || source.Category == "" || source.Version == "" {
			return sourceList{}, bundleFailure("bundled skill %d requires a valid uid, path, category, and version", index)
		}
		source.Path = cleanedPath
		identity := bundledSourceURL(cleanedPath)
		if _, exists := seenSources[identity]; exists {
			return sourceList{}, bundleFailure("duplicate source %s", identity)
		}
		if _, exists := seenUIDs[source.UID]; exists {
			return sourceList{}, bundleFailure("duplicate bundled skill uid %s", source.UID)
		}
		seenSources[identity] = struct{}{}
		seenUIDs[source.UID] = struct{}{}
	}
	return raw, nil
}

func resolveSourceInput(source sourceInput, sourcesRoot string) (sourceSpec, error) {
	if source.Bundled == nil {
		spec, err := resolveSource(source.URL)
		if err != nil {
			return sourceSpec{}, err
		}
		spec.FallbackName = source.FallbackName
		spec.Category = source.Category
		spec.Provider = source.Provider
		if source.RequiredVersion != "" {
			downloadURL, err := url.Parse(spec.ResolvedURL)
			if err != nil {
				return sourceSpec{}, err
			}
			if strings.EqualFold(downloadURL.Hostname(), "api.skillhub.cn") && downloadURL.Path == "/api/v1/download" {
				query := downloadURL.Query()
				query.Set("version", source.RequiredVersion)
				downloadURL.RawQuery = query.Encode()
				spec.ResolvedURL = downloadURL.String()
				spec.FallbackVersion = source.RequiredVersion
			}
		}
		return spec, nil
	}
	localPath := filepath.Join(sourcesRoot, filepath.FromSlash(source.Bundled.Path))
	info, err := os.Stat(localPath)
	if err != nil || !info.IsDir() {
		return sourceSpec{}, bundleFailure("bundled skill directory not found: %s", source.Bundled.Path)
	}
	sourceURL := bundledSourceURL(source.Bundled.Path)
	return sourceSpec{
		SourceURL: sourceURL, ResolvedURL: sourceURL, Identity: sourceURL,
		Key: filepath.Base(source.Bundled.Path), UID: source.Bundled.UID,
		Category: source.Bundled.Category, Version: source.Bundled.Version,
		Provider:  source.Bundled.Provider,
		LocalPath: localPath, FallbackName: source.FallbackName,
	}, nil
}

func featuredSourceInput(raw, requiredVersion, fallbackName, category string) (sourceInput, string, error) {
	source := strings.TrimSpace(raw)
	requiredVersion = strings.TrimSpace(requiredVersion)
	category = strings.TrimSpace(category)
	if _, err := resolveSource(source); err == nil {
		return sourceInput{URL: source, FallbackName: fallbackName, Category: category, RequiredVersion: requiredVersion}, source, nil
	}
	cleaned, err := skillpackage.CleanPath(source)
	if err != nil {
		return sourceInput{}, "", bundleFailure("invalid local Skill path %q", source)
	}
	bundled := &bundledSource{Path: cleaned, Category: category, Version: requiredVersion}
	return sourceInput{Bundled: bundled, FallbackName: fallbackName}, bundledSourceURL(cleaned), nil
}

func bundledSourceURL(relativePath string) string {
	return "builtin://" + filepath.ToSlash(relativePath)
}

func resolveSource(raw string) (sourceSpec, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" {
		return sourceSpec{}, bundleFailure("invalid source URL %q", raw)
	}
	parsed.Fragment = ""
	spec := sourceSpec{SourceURL: raw, ResolvedURL: parsed.String(), Identity: parsed.String()}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	host := strings.ToLower(parsed.Hostname())
	if (host == "skillhub.cn" || host == "www.skillhub.cn") && len(segments) >= 2 && segments[0] == "skills" {
		coordinate := segments[len(segments)-1]
		if len(segments) == 3 {
			coordinate = "@" + segments[1] + "/" + segments[2]
		} else if len(segments) != 2 {
			return sourceSpec{}, bundleFailure("unsupported SkillHub URL %q", raw)
		}
		download := &url.URL{Scheme: "https", Host: "api.skillhub.cn", Path: "/api/v1/download"}
		query := download.Query()
		query.Set("slug", coordinate)
		download.RawQuery = query.Encode()
		spec.ResolvedURL = download.String()
		spec.Identity = "skillhub:" + coordinate
		spec.Key = strings.TrimPrefix(coordinate, "@")
	}
	if spec.Key == "" {
		base := filepath.Base(parsed.Path)
		spec.Key = strings.TrimSuffix(base, filepath.Ext(base))
		if spec.Key == "" || spec.Key == "." {
			spec.Key = "skill"
		}
	}
	return spec, nil
}

type originPackage struct {
	ArchivePath string
	Files       map[string][]byte
	PackageRoot string
	ArchiveHash string
	TreeHash    string
	ArchiveSize int64
}

func resolveEntry(ctx context.Context, client *http.Client, spec sourceSpec, cacheDir string, patchCatalog skillpatch.Catalog) (skillbuiltin.CatalogSkill, string, []skillpatch.AppliedPatch, error) {
	if spec.LocalPath != "" {
		return resolveBundledEntry(spec, cacheDir, patchCatalog)
	}
	tempPath, err := download(ctx, client, spec.ResolvedURL, cacheDir)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	defer os.Remove(tempPath)
	hash, _, err := fileDigest(tempPath)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	cachePath := filepath.Join(cacheDir, hash+".zip")
	if err := copyFile(tempPath, cachePath); err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	origin, err := inspectOrigin(cachePath)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	return finalizeEntry(spec, origin, cacheDir, patchCatalog)
}

func inspectOrigin(archivePath string) (originPackage, error) {
	hash, size, err := fileDigest(archivePath)
	if err != nil {
		return originPackage{}, err
	}
	pkg, err := skillpackage.ReadZip(archivePath)
	if err != nil {
		return originPackage{}, err
	}
	files := pkg.Files
	return originPackage{
		ArchivePath: archivePath,
		Files:       files,
		PackageRoot: pkg.PackageRoot,
		ArchiveHash: hash,
		TreeHash:    skillpackage.TreeHash(files),
		ArchiveSize: size,
	}, nil
}

func finalizeEntry(spec sourceSpec, origin originPackage, cacheDir string, patchCatalog skillpatch.Catalog) (skillbuiltin.CatalogSkill, string, []skillpatch.AppliedPatch, error) {
	target := skillpatch.Target{
		UID:            resolvedSkillUID(spec),
		Version:        originSkillVersion(spec, origin.Files, origin.TreeHash),
		OriginTreeHash: origin.TreeHash,
	}
	patched, err := skillpatch.Apply(target, origin.Files, patchCatalog)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	archivePath := origin.ArchivePath
	if len(patched.AppliedPatches) > 0 {
		tempPath, err := skillpackage.WriteZip(patched.Files, cacheDir)
		if err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
		defer os.Remove(tempPath)
		hash, size, err := fileDigest(tempPath)
		if err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
		if size > maxArchiveBytes {
			return skillbuiltin.CatalogSkill{}, "", nil, bundleFailure("archive exceeds %d bytes", maxArchiveBytes)
		}
		archivePath = filepath.Join(cacheDir, hash+".zip")
		if err := copyFile(tempPath, archivePath); err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
	}
	entry, err := inspectEntry(spec, archivePath, patched.Files, origin.PackageRoot, patched.SkillMDPatched)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	if len(patched.AppliedPatches) > 0 {
		entry.OriginArchiveSHA256 = origin.ArchiveHash
		entry.OriginTreeSHA256 = origin.TreeHash
		entry.OriginArchiveSize = origin.ArchiveSize
		entry.PatchSetSHA256 = patched.PatchSetSHA256
		entry.AppliedPatches = catalogPatches(patched.AppliedPatches)
	}
	return entry, archivePath, patched.AppliedPatches, nil
}

func originSkillVersion(spec sourceSpec, files map[string][]byte, treeHash string) string {
	metadataVersion := ""
	if content, ok := files["SKILL.md"]; ok {
		if meta, err := skillmetadata.Parse(content); err == nil {
			metadataVersion = meta.Version
		}
	}
	return resolvedSkillVersion(spec, metadataVersion, files, treeHash)
}

func inspectEntry(spec sourceSpec, archivePath string, files map[string][]byte, packageRoot string, strictSkillMD bool) (skillbuiltin.CatalogSkill, error) {
	hash, size, err := fileDigest(archivePath)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, err
	}
	content, ok := files["SKILL.md"]
	if !ok {
		return skillbuiltin.CatalogSkill{}, bundleFailure("skill package must contain SKILL.md")
	}
	uid := resolvedSkillUID(spec)
	var resolved skillmetadata.Resolved
	if strictSkillMD {
		meta, err := skillmetadata.ParseRequired(content)
		if err != nil {
			return skillbuiltin.CatalogSkill{}, err
		}
		resolved = skillmetadata.Resolved{Metadata: meta, Content: content}
	} else {
		resolved, err = skillmetadata.Resolve(content, spec.FallbackName, packageRoot, spec.Key, "lazymind-skill-"+uid)
	}
	if err != nil {
		return skillbuiltin.CatalogSkill{}, err
	}
	treeHash := skillpackage.TreeHash(files)
	version := resolvedSkillVersion(spec, resolved.Version, files, treeHash)
	category := spec.Category
	if category == "" {
		category = resolved.Category
	}
	if category == "" {
		category = "external"
	}
	return skillbuiltin.CatalogSkill{
		Key:           spec.Key,
		UID:           uid,
		SourceURL:     spec.SourceURL,
		ResolvedURL:   spec.ResolvedURL,
		Version:       version,
		Name:          resolved.Name,
		Description:   resolved.Description,
		Category:      category,
		Tags:          resolved.Tags,
		Provider:      spec.Provider,
		Content:       string(resolved.Content),
		ArchiveSHA256: hash,
		TreeSHA256:    treeHash,
		ArchiveSize:   size,
	}, nil
}

func resolvedSkillVersion(spec sourceSpec, metadataVersion string, files map[string][]byte, treeHash string) string {
	version := strings.TrimSpace(spec.Version)
	if version == "" {
		version = strings.TrimSpace(metadataVersion)
	}
	if version == "" {
		var raw packageMeta
		_ = json.Unmarshal(files["_meta.json"], &raw)
		version = strings.TrimSpace(raw.Version)
	}
	if version == "" {
		version = strings.TrimSpace(spec.FallbackVersion)
	}
	if version == "" {
		version = "0.0.0+" + treeHash[:12]
	}
	return version
}

func resolvedSkillUID(spec sourceSpec) string {
	if spec.UID != "" {
		return spec.UID
	}
	identityHash := sha256.Sum256([]byte(spec.Identity))
	return "bsk_" + strings.ToUpper(hex.EncodeToString(identityHash[:14]))
}

func catalogPatches(patches []skillpatch.AppliedPatch) []skillbuiltin.CatalogPatch {
	out := make([]skillbuiltin.CatalogPatch, 0, len(patches))
	for _, patch := range patches {
		out = append(out, skillbuiltin.CatalogPatch{ID: patch.ID, SHA256: patch.SHA256})
	}
	return out
}

func resolveBundledEntry(spec sourceSpec, cacheDir string, patchCatalog skillpatch.Catalog) (skillbuiltin.CatalogSkill, string, []skillpatch.AppliedPatch, error) {
	files, err := readBundledFiles(spec.LocalPath)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	tempPath, err := skillpackage.WriteZip(files, cacheDir)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	defer os.Remove(tempPath)
	hash, size, err := fileDigest(tempPath)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	if size > maxArchiveBytes {
		return skillbuiltin.CatalogSkill{}, "", nil, bundleFailure("archive exceeds %d bytes", maxArchiveBytes)
	}
	cachePath := filepath.Join(cacheDir, hash+".zip")
	if err := copyFile(tempPath, cachePath); err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	origin := originPackage{
		ArchivePath: cachePath,
		Files:       files,
		PackageRoot: "",
		ArchiveHash: hash,
		TreeHash:    skillpackage.TreeHash(files),
		ArchiveSize: size,
	}
	return finalizeEntry(spec, origin, cacheDir, patchCatalog)
}

func readBundledFiles(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	var total int64
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return bundleFailure("bundled skill cannot contain symlink %s", filePath)
		}
		if entry.IsDir() {
			return nil
		}
		if len(files) >= skillpackage.MaxFiles {
			return bundleFailure("bundled skill contains too many files")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > skillpackage.MaxFileBytes {
			return bundleFailure("bundled skill file %s exceeds %d bytes", filePath, skillpackage.MaxFileBytes)
		}
		total += info.Size()
		if total > skillpackage.MaxTotalBytes {
			return bundleFailure("bundled skill exceeds %d bytes", skillpackage.MaxTotalBytes)
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		cleaned, err := skillpackage.CleanPath(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		body, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		files[cleaned] = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func materializeFrozen(ctx context.Context, client *http.Client, spec sourceSpec, locked skillbuiltin.CatalogSkill, cacheDir string, patchCatalog skillpatch.Catalog) (skillbuiltin.CatalogSkill, string, []skillpatch.AppliedPatch, error) {
	if spec.LocalPath != "" {
		current, archivePath, appliedPatches, err := resolveBundledEntry(spec, cacheDir, patchCatalog)
		if err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
		if err := validateFrozenEntry(current, locked); err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, bundleFailure("bundled skill does not match frozen lock: %v", err)
		}
		return current, archivePath, appliedPatches, validateLockedArchive(archivePath, locked)
	}
	expectedOriginHash := locked.ArchiveSHA256
	expectedOriginSize := locked.ArchiveSize
	expectedOriginTree := locked.TreeSHA256
	if len(locked.AppliedPatches) > 0 {
		expectedOriginHash = locked.OriginArchiveSHA256
		expectedOriginSize = locked.OriginArchiveSize
		expectedOriginTree = locked.OriginTreeSHA256
	}
	originPath := filepath.Join(cacheDir, expectedOriginHash+".zip")
	if hash, size, err := fileDigest(originPath); err != nil || hash != expectedOriginHash || expectedOriginSize > 0 && size != expectedOriginSize {
		tempPath, err := download(ctx, client, spec.ResolvedURL, cacheDir)
		if err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
		defer os.Remove(tempPath)
		hash, size, err := fileDigest(tempPath)
		if err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
		if hash != expectedOriginHash || expectedOriginSize > 0 && size != expectedOriginSize {
			return skillbuiltin.CatalogSkill{}, "", nil, bundleFailure("download does not match frozen lock origin")
		}
		if err := copyFile(tempPath, originPath); err != nil {
			return skillbuiltin.CatalogSkill{}, "", nil, err
		}
	}
	origin, err := inspectOrigin(originPath)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	if origin.TreeHash != expectedOriginTree {
		return skillbuiltin.CatalogSkill{}, "", nil, bundleFailure("origin package tree does not match frozen lock")
	}
	current, archivePath, appliedPatches, err := finalizeEntry(spec, origin, cacheDir, patchCatalog)
	if err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	if err := validateFrozenEntry(current, locked); err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	if err := validateLockedArchive(archivePath, locked); err != nil {
		return skillbuiltin.CatalogSkill{}, "", nil, err
	}
	return current, archivePath, appliedPatches, nil
}

func validateFrozenEntry(current, locked skillbuiltin.CatalogSkill) error {
	if current.UID != locked.UID || current.Version != locked.Version || current.Provider != locked.Provider || current.ArchiveSHA256 != locked.ArchiveSHA256 || current.TreeSHA256 != locked.TreeSHA256 || current.ArchiveSize != locked.ArchiveSize {
		return bundleFailure("final package metadata changed")
	}
	if current.OriginArchiveSHA256 != locked.OriginArchiveSHA256 || current.OriginTreeSHA256 != locked.OriginTreeSHA256 || current.OriginArchiveSize != locked.OriginArchiveSize || current.PatchSetSHA256 != locked.PatchSetSHA256 {
		return bundleFailure("patch provenance changed")
	}
	if len(current.AppliedPatches) != len(locked.AppliedPatches) {
		return bundleFailure("applied patch count changed")
	}
	for index := range current.AppliedPatches {
		if current.AppliedPatches[index] != locked.AppliedPatches[index] {
			return bundleFailure("applied patch provenance changed")
		}
	}
	return nil
}

func validateLockedArchive(path string, locked skillbuiltin.CatalogSkill) error {
	pkg, err := skillpackage.ReadZip(path)
	if err != nil {
		return err
	}
	files := pkg.Files
	if skillpackage.TreeHash(files) != locked.TreeSHA256 {
		return bundleFailure("package tree does not match frozen lock")
	}
	if locked.Content != "" && string(files["SKILL.md"]) != locked.Content {
		return bundleFailure("SKILL.md does not match frozen lock")
	}
	return nil
}

func download(ctx context.Context, client *http.Client, rawURL, cacheDir string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		path, err := downloadOnce(ctx, client, rawURL, cacheDir)
		if err == nil {
			return path, nil
		}
		lastErr = err
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	return "", lastErr
}

func downloadOnce(ctx context.Context, client *http.Client, rawURL, cacheDir string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "LazyMind-Builtin-Skill-Bundler/1")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", bundleFailure("download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxArchiveBytes {
		return "", bundleFailure("archive exceeds %d bytes", maxArchiveBytes)
	}
	file, err := os.CreateTemp(cacheDir, ".download-*.zip")
	if err != nil {
		return "", err
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	written, err := io.Copy(file, io.LimitReader(response.Body, maxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if written > maxArchiveBytes {
		return "", bundleFailure("archive exceeds %d bytes", maxArchiveBytes)
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	keep = true
	return path, nil
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout: 90 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return bundleError("too many redirects")
			}
			return nil
		},
	}
}

func fileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func prepareOutput(outputPath, expectedName, managedDirectory string) error {
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs) + string(filepath.Separator)
	if abs == volume || filepath.Dir(abs) == abs || !strings.Contains(strings.ToLower(filepath.Base(abs)), expectedName) {
		return bundleFailure("refusing to replace broad output path %s", abs)
	}
	if err := os.RemoveAll(filepath.Join(abs, managedDirectory)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(abs, "catalog.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(filepath.Join(abs, managedDirectory), 0o755)
}

func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func writeJSONAtomic(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tempPath, path)
}

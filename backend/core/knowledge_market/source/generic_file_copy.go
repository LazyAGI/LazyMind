package source

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const AdapterGenericFileCopy = "generic_file_copy"

type genericFileCopyConfig struct {
	Include              []string       `yaml:"include"`
	Exclude              []string       `yaml:"exclude"`
	Extensions           []string       `yaml:"extensions"`
	DisplayNameTemplate  string         `yaml:"display_name_template"`
	RelativePathTemplate string         `yaml:"relative_path_template"`
	Tags                 []string       `yaml:"tags"`
	Groups               []genericGroup `yaml:"groups"`
}

type genericFileCopyAdapter struct {
	rule                 *genericMatchRule
	displayNameTemplate  string
	relativePathTemplate string
	tags                 []string
}

func init() {
	Register(AdapterGenericFileCopy, newGenericFileCopyAdapter)
}

func newGenericFileCopyAdapter(options Options) (Adapter, error) {
	var config genericFileCopyConfig
	if err := decodeStrictOptions(options, &config, AdapterGenericFileCopy); err != nil {
		return nil, err
	}
	config.DisplayNameTemplate = strings.TrimSpace(config.DisplayNameTemplate)
	config.RelativePathTemplate = strings.TrimSpace(config.RelativePathTemplate)
	if config.DisplayNameTemplate == "" {
		config.DisplayNameTemplate = "{{basename}}"
	}
	if config.RelativePathTemplate == "" {
		config.RelativePathTemplate = "{{dir}}"
	}
	if err := validateTemplateSyntax(config.DisplayNameTemplate); err != nil {
		return nil, fmt.Errorf("display_name_template: %w", err)
	}
	if err := validateTemplateSyntax(config.RelativePathTemplate); err != nil {
		return nil, fmt.Errorf("relative_path_template: %w", err)
	}
	for _, tag := range config.Tags {
		if err := validateTemplateSyntax(tag); err != nil {
			return nil, fmt.Errorf("tag template: %w", err)
		}
	}
	rule, err := newGenericMatchRule(config.Include, config.Exclude, config.Extensions, config.Groups)
	if err != nil {
		return nil, err
	}
	return &genericFileCopyAdapter{
		rule:                 rule,
		displayNameTemplate:  config.DisplayNameTemplate,
		relativePathTemplate: config.RelativePathTemplate,
		tags:                 config.Tags,
	}, nil
}

func (a *genericFileCopyAdapter) ID() string { return AdapterGenericFileCopy }

func (a *genericFileCopyAdapter) Match(path string) bool {
	return a.rule.matches(path)
}

func (a *genericFileCopyAdapter) Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error) {
	units := make([]IngestUnit, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !a.rule.matches(file.Path) {
			continue
		}
		group, grouped := a.rule.resolveGroup(file.Path)
		groupName := ""
		displayTemplate := a.displayNameTemplate
		relativeTemplate := a.relativePathTemplate
		tags := a.tags
		if grouped {
			groupName = group.Name
			if group.DisplayNameTemplate != "" {
				displayTemplate = group.DisplayNameTemplate
			}
			if group.RelativePathTemplate != "" {
				relativeTemplate = group.RelativePathTemplate
			}
			if group.Tags != nil {
				tags = group.Tags
			}
		}
		fileContext := newGenericFileContext(file, group.StripPrefix, groupName)
		values := fileContext.values()
		displayName, err := renderTemplate(displayTemplate, values, false)
		if err != nil {
			return nil, fmt.Errorf("render display name for %s: %w", file.Path, err)
		}
		displayName = cleanDisplayName(displayName)
		relativePath, err := renderTemplate(relativeTemplate, values, false)
		if err != nil {
			return nil, fmt.Errorf("render relative path for %s: %w", file.Path, err)
		}
		relativePath = cleanRelativePath(relativePath)
		renderedTags, err := renderTags(tags, values, false)
		if err != nil {
			return nil, fmt.Errorf("render tags for %s: %w", file.Path, err)
		}
		units = append(units, IngestUnit{
			LocalPath:    filepath.Join(root, filepath.FromSlash(file.Path)),
			DisplayName:  displayName,
			RelativePath: relativePath,
			Tags:         renderedTags,
		})
	}
	return units, nil
}

package currentmemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

const publicPatchCASAttempts = 3

var (
	ErrInvalidRequest  = errors.New("invalid current memory request")
	ErrCorruptDocument = errors.New("stored current memory document is invalid")
)

type ETagConflictError struct {
	CurrentETag string
}

func (e *ETagConflictError) Error() string {
	return "preference etag conflict"
}

func (e *ETagConflictError) Unwrap() error {
	return ErrConflict
}

type Module struct {
	repository *Repository
	clock      func() time.Time
}

func NewModule(db *gorm.DB) *Module {
	return &Module{
		repository: NewRepository(db),
		clock:      time.Now,
	}
}

func (m *Module) GetSoul(ctx context.Context, userID string) (CurrentMemorySoulData, error) {
	document, entry, err := readTypedDocument(
		ctx,
		m,
		userID,
		SoulPath,
		ParseSoul,
	)
	if err != nil {
		return CurrentMemorySoulData{}, err
	}
	return CurrentMemorySoulData{
		Document:  document,
		UpdatedAt: formatUpdatedAt(entry.UpdatedAt),
	}, nil
}

func (m *Module) PatchSoul(
	ctx context.Context,
	userID string,
	patch map[string]any,
) (CurrentMemorySoulData, error) {
	document, updatedAt, err := patchTypedDocument(
		ctx,
		m,
		userID,
		SoulPath,
		patch,
		ParseSoul,
		RenderSoul,
	)
	if err != nil {
		return CurrentMemorySoulData{}, err
	}
	return CurrentMemorySoulData{
		Document:  document,
		UpdatedAt: formatUpdatedAt(updatedAt),
	}, nil
}

func (m *Module) GetProfile(
	ctx context.Context,
	userID string,
) (CurrentMemoryProfileData, error) {
	document, entry, err := readTypedDocument(
		ctx,
		m,
		userID,
		ProfilePath,
		ParseProfile,
	)
	if err != nil {
		return CurrentMemoryProfileData{}, err
	}
	return CurrentMemoryProfileData{
		Document:  document,
		UpdatedAt: formatUpdatedAt(entry.UpdatedAt),
	}, nil
}

func (m *Module) PatchProfile(
	ctx context.Context,
	userID string,
	patch map[string]any,
) (CurrentMemoryProfileData, error) {
	document, updatedAt, err := patchTypedDocument(
		ctx,
		m,
		userID,
		ProfilePath,
		patch,
		ParseProfile,
		RenderProfile,
	)
	if err != nil {
		return CurrentMemoryProfileData{}, err
	}
	return CurrentMemoryProfileData{
		Document:  document,
		UpdatedAt: formatUpdatedAt(updatedAt),
	}, nil
}

func (m *Module) ListPreferences(
	ctx context.Context,
	userID string,
) (CurrentMemoryPreferenceListData, error) {
	document, entry, err := readTypedDocument(
		ctx,
		m,
		userID,
		PreferencePath,
		ParsePreferences,
	)
	if err != nil {
		return CurrentMemoryPreferenceListData{}, err
	}
	return preferenceListData(document, entry), nil
}

func (m *Module) GetPreference(
	ctx context.Context,
	userID string,
	name string,
) (CurrentMemoryPreferenceDetailData, error) {
	if strings.TrimSpace(name) == "" {
		return CurrentMemoryPreferenceDetailData{}, fmt.Errorf(
			"%w: preference name is required",
			ErrInvalidRequest,
		)
	}
	document, _, err := readTypedDocument(
		ctx,
		m,
		userID,
		PreferencePath,
		ParsePreferences,
	)
	if err != nil {
		return CurrentMemoryPreferenceDetailData{}, err
	}
	var target *PreferenceItem
	for index := range document.Preferences {
		if document.Preferences[index].Name == name {
			target = &document.Preferences[index]
			break
		}
	}
	if target == nil {
		return CurrentMemoryPreferenceDetailData{}, ErrNotFound
	}
	result := CurrentMemoryPreferenceDetailData{
		Item:            publicPreferenceItem(*target),
		ReferenceStatus: "missing",
		Reference:       nil,
	}
	referencePath, _, err := SplitReferenceRef(target.Ref)
	if err != nil {
		return CurrentMemoryPreferenceDetailData{}, fmt.Errorf(
			"%w: %v",
			ErrCorruptDocument,
			err,
		)
	}
	entry, err := m.repository.GetEntry(ctx, userID, referencePath)
	if errors.Is(err, ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return CurrentMemoryPreferenceDetailData{}, err
	}
	if entry.EntryType != EntryFile {
		return CurrentMemoryPreferenceDetailData{}, fmt.Errorf(
			"%w: %s is not a file",
			ErrCorruptDocument,
			referencePath,
		)
	}
	reference, err := ParseReference(entry.Content)
	if err != nil {
		return CurrentMemoryPreferenceDetailData{}, fmt.Errorf(
			"%w: %v",
			ErrCorruptDocument,
			err,
		)
	}
	result.ReferenceStatus = "available"
	result.Reference = &reference
	return result, nil
}

func (m *Module) ReorderPreferences(
	ctx context.Context,
	userID string,
	request CurrentMemoryPreferenceOrderRequest,
) (CurrentMemoryPreferenceListData, error) {
	request.ExpectedETag = strings.TrimSpace(request.ExpectedETag)
	if request.ExpectedETag == "" {
		return CurrentMemoryPreferenceListData{}, fmt.Errorf(
			"%w: expected_etag is required",
			ErrInvalidRequest,
		)
	}
	if request.OrderedNames == nil {
		return CurrentMemoryPreferenceListData{}, fmt.Errorf(
			"%w: ordered_names is required",
			ErrInvalidRequest,
		)
	}
	orderedNames, err := normalizeOrderedNames(request.OrderedNames)
	if err != nil {
		return CurrentMemoryPreferenceListData{}, err
	}
	if err := m.repository.EnsureInitialized(ctx, userID); err != nil {
		return CurrentMemoryPreferenceListData{}, err
	}
	var result CurrentMemoryPreferenceListData
	err = m.repository.Transaction(ctx, func(repository *Repository) error {
		entry, getErr := repository.GetEntryForUpdate(ctx, userID, PreferencePath)
		if getErr != nil {
			return getErr
		}
		currentETag := ContentETag(entry.Content)
		if request.ExpectedETag != currentETag {
			return &ETagConflictError{CurrentETag: currentETag}
		}
		document, parseErr := ParsePreferences(entry.Content)
		if parseErr != nil {
			return fmt.Errorf("%w: %v", ErrCorruptDocument, parseErr)
		}
		reordered, reorderErr := reorderPreferenceItems(document.Preferences, orderedNames)
		if reorderErr != nil {
			return reorderErr
		}
		content, renderErr := RenderPreferences(
			PreferenceDocument{Preferences: reordered},
		)
		if renderErr != nil {
			return renderErr
		}
		now := m.now()
		if updateErr := repository.UpdateFileContent(
			ctx,
			userID,
			PreferencePath,
			content,
			now,
		); updateErr != nil {
			return updateErr
		}
		entry.Content = content
		entry.Size = int64(len(content))
		entry.UpdatedAt = now
		result = preferenceListData(
			PreferenceDocument{Preferences: reordered},
			entry,
		)
		return nil
	})
	return result, err
}

func (m *Module) DeletePreference(
	ctx context.Context,
	userID string,
	name string,
) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: preference name is required", ErrInvalidRequest)
	}
	if err := m.repository.EnsureInitialized(ctx, userID); err != nil {
		return err
	}
	return m.repository.Transaction(ctx, func(repository *Repository) error {
		entry, err := repository.GetEntryForUpdate(ctx, userID, PreferencePath)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		document, err := ParsePreferences(entry.Content)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorruptDocument, err)
		}
		index := -1
		for itemIndex := range document.Preferences {
			if document.Preferences[itemIndex].Name == name {
				index = itemIndex
				break
			}
		}
		if index < 0 {
			return nil
		}
		target := document.Preferences[index]
		remaining := append(
			append([]PreferenceItem{}, document.Preferences[:index]...),
			document.Preferences[index+1:]...,
		)
		content, err := RenderPreferences(
			PreferenceDocument{Preferences: remaining},
		)
		if err != nil {
			return err
		}
		if err := repository.UpdateFileContent(
			ctx,
			userID,
			PreferencePath,
			content,
			m.now(),
		); err != nil {
			return err
		}
		targetPath, _, err := SplitReferenceRef(target.Ref)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorruptDocument, err)
		}
		for _, item := range remaining {
			remainingPath, _, splitErr := SplitReferenceRef(item.Ref)
			if splitErr != nil {
				return fmt.Errorf("%w: %v", ErrCorruptDocument, splitErr)
			}
			if remainingPath == targetPath {
				return nil
			}
		}
		return repository.DeletePath(ctx, userID, targetPath)
	})
}

func readTypedDocument[T any](
	ctx context.Context,
	module *Module,
	userID string,
	entryPath string,
	parse func([]byte) (T, error),
) (T, orm.MemoryCurrentEntry, error) {
	var zero T
	entry, err := module.readFile(ctx, userID, entryPath)
	if err != nil {
		return zero, orm.MemoryCurrentEntry{}, err
	}
	document, err := parse(entry.Content)
	if err != nil {
		return zero, orm.MemoryCurrentEntry{}, fmt.Errorf(
			"%w: %v",
			ErrCorruptDocument,
			err,
		)
	}
	return document, entry, nil
}

func patchTypedDocument[T any](
	ctx context.Context,
	module *Module,
	userID string,
	entryPath string,
	patch map[string]any,
	parse func([]byte) (T, error),
	render func(T) ([]byte, error),
) (T, time.Time, error) {
	var zero T
	if countPatchLeaves(patch) == 0 {
		return zero, time.Time{}, fmt.Errorf(
			"%w: at least one field is required",
			ErrInvalidRequest,
		)
	}
	for attempt := 0; attempt < publicPatchCASAttempts; attempt++ {
		entry, err := module.readFile(ctx, userID, entryPath)
		if err != nil {
			return zero, time.Time{}, err
		}
		if _, err := parse(entry.Content); err != nil {
			return zero, time.Time{}, fmt.Errorf(
				"%w: %v",
				ErrCorruptDocument,
				err,
			)
		}
		var current map[string]any
		if err := yaml.Unmarshal(entry.Content, &current); err != nil {
			return zero, time.Time{}, fmt.Errorf(
				"%w: %v",
				ErrCorruptDocument,
				err,
			)
		}
		mergePatchMapping(current, patch)
		mergedContent, err := yaml.Marshal(current)
		if err != nil {
			return zero, time.Time{}, err
		}
		document, err := parse(mergedContent)
		if err != nil {
			return zero, time.Time{}, fmt.Errorf(
				"%w: %v",
				ErrInvalidRequest,
				err,
			)
		}
		content, err := render(document)
		if err != nil {
			return zero, time.Time{}, fmt.Errorf(
				"%w: %v",
				ErrInvalidRequest,
				err,
			)
		}
		now := module.now()
		updated, err := module.repository.CompareAndSwapFileContent(
			ctx,
			userID,
			entryPath,
			entry.Content,
			content,
			now,
		)
		if err != nil {
			return zero, time.Time{}, err
		}
		if updated {
			return document, now, nil
		}
	}
	return zero, time.Time{}, ErrConflict
}

func (m *Module) readFile(
	ctx context.Context,
	userID string,
	entryPath string,
) (orm.MemoryCurrentEntry, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return orm.MemoryCurrentEntry{}, fmt.Errorf(
			"%w: user_id is required",
			ErrInvalidRequest,
		)
	}
	if m == nil || m.repository == nil {
		return orm.MemoryCurrentEntry{}, errors.New("memory module is not configured")
	}
	if err := m.repository.EnsureInitialized(ctx, userID); err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	entry, err := m.repository.GetEntry(ctx, userID, entryPath)
	if err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	if entry.EntryType != EntryFile {
		return orm.MemoryCurrentEntry{}, fmt.Errorf(
			"%w: %s is not a file",
			ErrCorruptDocument,
			entryPath,
		)
	}
	return entry, nil
}

func preferenceListData(
	document PreferenceDocument,
	entry orm.MemoryCurrentEntry,
) CurrentMemoryPreferenceListData {
	items := make([]CurrentMemoryPreferenceItem, 0, len(document.Preferences))
	for _, item := range document.Preferences {
		items = append(items, publicPreferenceItem(item))
	}
	return CurrentMemoryPreferenceListData{
		Items:     items,
		TotalSize: int64(len(items)),
		ETag:      ContentETag(entry.Content),
		UpdatedAt: formatUpdatedAt(entry.UpdatedAt),
	}
}

func publicPreferenceItem(item PreferenceItem) CurrentMemoryPreferenceItem {
	return CurrentMemoryPreferenceItem{
		Name:      item.Name,
		Summary:   item.Summary,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func normalizeOrderedNames(names []string) ([]string, error) {
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf(
				"%w: ordered_names must contain every preference name",
				ErrInvalidRequest,
			)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf(
				"%w: ordered_names must not contain duplicates",
				ErrInvalidRequest,
			)
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized, nil
}

func reorderPreferenceItems(
	items []PreferenceItem,
	orderedNames []string,
) ([]PreferenceItem, error) {
	if len(items) != len(orderedNames) {
		return nil, fmt.Errorf(
			"%w: ordered_names must be an exact permutation of existing preferences",
			ErrInvalidRequest,
		)
	}
	byName := make(map[string]PreferenceItem, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}
	result := make([]PreferenceItem, 0, len(items))
	for _, name := range orderedNames {
		item, exists := byName[name]
		if !exists {
			return nil, fmt.Errorf(
				"%w: ordered_names must be an exact permutation of existing preferences",
				ErrInvalidRequest,
			)
		}
		result = append(result, item)
	}
	return result, nil
}

func mergePatchMapping(current map[string]any, patch map[string]any) {
	for key, patchValue := range patch {
		patchMapping, isMapping := patchValue.(map[string]any)
		currentMapping, currentIsMapping := current[key].(map[string]any)
		if isMapping && currentIsMapping {
			mergePatchMapping(currentMapping, patchMapping)
			continue
		}
		current[key] = patchValue
	}
}

func countPatchLeaves(patch map[string]any) int {
	count := 0
	for _, value := range patch {
		if nested, ok := value.(map[string]any); ok {
			count += countPatchLeaves(nested)
			continue
		}
		count++
	}
	return count
}

func formatUpdatedAt(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func (m *Module) now() time.Time {
	if m != nil && m.clock != nil {
		return m.clock().UTC()
	}
	return time.Now().UTC()
}

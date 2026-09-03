package currentmemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	repository                *Repository
	clock                     func() time.Time
	preferenceContextMaxChars int
}

func NewModule(db *gorm.DB) *Module {
	return NewModuleWithPreferenceContextMaxChars(
		db,
		mustPreferenceContextMaxCharsFromEnv(),
	)
}

func NewModuleWithPreferenceContextMaxChars(db *gorm.DB, maxChars int) *Module {
	if maxChars <= 0 {
		panic("preference context max chars must be positive")
	}
	return &Module{
		repository:                NewRepository(db),
		clock:                     time.Now,
		preferenceContextMaxChars: maxChars,
	}
}

func (m *Module) GetSoul(ctx context.Context, userID string) (CurrentMemorySoulData, error) {
	document, entry, err := readNormalizedDocument(
		ctx,
		m,
		userID,
		SoulPath,
		NormalizeSoul,
	)
	if err != nil {
		return CurrentMemorySoulData{}, err
	}
	return CurrentMemorySoulData{
		Document:        document,
		TemplateVersion: soulTemplate.SchemaVersion,
		Presentation:    soulTemplate.Presentation,
		UpdatedAt:       formatUpdatedAt(entry.UpdatedAt),
	}, nil
}

func (m *Module) PatchSoul(
	ctx context.Context,
	userID string,
	request CurrentMemoryOperationsRequest,
) (CurrentMemorySoulData, error) {
	if _, err := m.EnsureLatestEntry(ctx, userID, SoulPath); err != nil {
		return CurrentMemorySoulData{}, err
	}
	document, updatedAt, err := applyOperationsToDocument(
		ctx,
		m,
		userID,
		SoulPath,
		request.Operations,
		NormalizeSoul,
		RenderSoul,
		applySoulOperations,
	)
	if err != nil {
		return CurrentMemorySoulData{}, err
	}
	return CurrentMemorySoulData{
		Document:        document,
		TemplateVersion: soulTemplate.SchemaVersion,
		Presentation:    soulTemplate.Presentation,
		UpdatedAt:       formatUpdatedAt(updatedAt),
	}, nil
}

func (m *Module) GetProfile(
	ctx context.Context,
	userID string,
) (CurrentMemoryProfileData, error) {
	document, entry, err := readNormalizedDocument(
		ctx,
		m,
		userID,
		ProfilePath,
		NormalizeProfile,
	)
	if err != nil {
		return CurrentMemoryProfileData{}, err
	}
	return CurrentMemoryProfileData{
		Document:        document,
		TemplateVersion: profileTemplate.SchemaVersion,
		Presentation:    profileTemplate.Presentation,
		UpdatedAt:       formatUpdatedAt(entry.UpdatedAt),
	}, nil
}

func (m *Module) PatchProfile(
	ctx context.Context,
	userID string,
	request CurrentMemoryOperationsRequest,
) (CurrentMemoryProfileData, error) {
	if _, err := m.EnsureLatestEntry(ctx, userID, ProfilePath); err != nil {
		return CurrentMemoryProfileData{}, err
	}
	document, updatedAt, err := applyOperationsToDocument(
		ctx,
		m,
		userID,
		ProfilePath,
		request.Operations,
		NormalizeProfile,
		RenderProfile,
		applyProfileOperations,
	)
	if err != nil {
		return CurrentMemoryProfileData{}, err
	}
	return CurrentMemoryProfileData{
		Document:        document,
		TemplateVersion: profileTemplate.SchemaVersion,
		Presentation:    profileTemplate.Presentation,
		UpdatedAt:       formatUpdatedAt(updatedAt),
	}, nil
}

func (m *Module) EnsureLatestEntry(
	ctx context.Context,
	userID string,
	entryPath string,
) (orm.MemoryCurrentEntry, error) {
	switch entryPath {
	case SoulPath:
		_, entry, err := readNormalizedDocument(ctx, m, userID, SoulPath, NormalizeSoul)
		return entry, err
	case ProfilePath:
		_, entry, err := readNormalizedDocument(ctx, m, userID, ProfilePath, NormalizeProfile)
		return entry, err
	default:
		return m.readFile(ctx, userID, entryPath)
	}
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
	return preferenceListData(document, entry, m.preferenceContextMaxChars), nil
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
	err = m.repository.PreferenceTransaction(ctx, userID, func(repository *Repository) error {
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
			m.preferenceContextMaxChars,
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
	return m.repository.PreferenceTransaction(ctx, userID, func(repository *Repository) error {
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

func readNormalizedDocument[T any](
	ctx context.Context,
	module *Module,
	userID string,
	entryPath string,
	normalize func([]byte) (T, []byte, error),
) (T, orm.MemoryCurrentEntry, error) {
	var zero T
	for attempt := 0; attempt < publicPatchCASAttempts; attempt++ {
		entry, err := module.readFile(ctx, userID, entryPath)
		if err != nil {
			return zero, orm.MemoryCurrentEntry{}, err
		}
		document, content, err := normalize(entry.Content)
		if err != nil {
			return zero, orm.MemoryCurrentEntry{}, fmt.Errorf(
				"%w: %v",
				ErrCorruptDocument,
				err,
			)
		}
		if string(content) == string(entry.Content) {
			return document, entry, nil
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
			return zero, orm.MemoryCurrentEntry{}, err
		}
		if updated {
			entry.Content = content
			entry.Size = int64(len(content))
			entry.UpdatedAt = now
			return document, entry, nil
		}
	}
	return zero, orm.MemoryCurrentEntry{}, ErrConflict
}

func applyOperationsToDocument[T any](
	ctx context.Context,
	module *Module,
	userID string,
	entryPath string,
	operations []CurrentMemoryOperation,
	normalize func([]byte) (T, []byte, error),
	render func(T) ([]byte, error),
	apply func(T, []CurrentMemoryOperation) (T, error),
) (T, time.Time, error) {
	var zero T
	if len(operations) == 0 {
		return zero, time.Time{}, fmt.Errorf(
			"%w: at least one operation is required",
			ErrInvalidRequest,
		)
	}
	for attempt := 0; attempt < publicPatchCASAttempts; attempt++ {
		entry, err := module.readFile(ctx, userID, entryPath)
		if err != nil {
			return zero, time.Time{}, err
		}
		document, _, err := normalize(entry.Content)
		if err != nil {
			return zero, time.Time{}, fmt.Errorf(
				"%w: %v",
				ErrCorruptDocument,
				err,
			)
		}
		document, err = apply(document, operations)
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

func applySoulOperations(
	document SoulDocument,
	operations []CurrentMemoryOperation,
) (SoulDocument, error) {
	return applyMemoryOperations(document, operations, "soul")
}

func applyProfileOperations(
	document ProfileDocument,
	operations []CurrentMemoryOperation,
) (ProfileDocument, error) {
	return applyMemoryOperations(document, operations, "profile")
}

func applyMemoryOperations(
	document MemoryDocument,
	operations []CurrentMemoryOperation,
	label string,
) (MemoryDocument, error) {
	next := cloneDocument(document)
	for _, operation := range operations {
		path := strings.TrimSpace(operation.Path)
		if path == "" || path == "schema_version" {
			return nil, fmt.Errorf("unsupported %s operation path %q", label, path)
		}
		contractValue, exists := nestedValue(document, path)
		if !exists {
			return nil, fmt.Errorf("unsupported %s operation path %q", label, path)
		}
		op := strings.TrimSpace(operation.Op)
		var updated any
		switch contractValue.(type) {
		case string:
			switch op {
			case "set":
				value, err := requiredOperationValue(operation)
				if err != nil {
					return nil, err
				}
				updated = value
			case "clear":
				if operation.Value != nil {
					return nil, fmt.Errorf("clear operation on %q must not include value", path)
				}
				updated = ""
			default:
				return nil, fmt.Errorf("%s string path %q only supports set or clear", label, path)
			}
		case nil:
			switch op {
			case "set":
				value, err := requiredOperationValue(operation)
				if err != nil {
					return nil, err
				}
				updated = value
			case "clear":
				if operation.Value != nil {
					return nil, fmt.Errorf("clear operation on %q must not include value", path)
				}
				updated = nil
			default:
				return nil, fmt.Errorf("%s null path %q only supports set or clear", label, path)
			}
		case []any:
			current, ok := nestedValue(next, path)
			if !ok {
				return nil, fmt.Errorf("unsupported %s operation path %q", label, path)
			}
			values, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("%s list path %q changed type", label, path)
			}
			value, err := applyStringListOperation(values, operation, label)
			if err != nil {
				return nil, err
			}
			updated = value
		default:
			return nil, fmt.Errorf("unsupported %s operation path %q", label, path)
		}
		if !setNestedValue(next, path, updated) {
			return nil, fmt.Errorf("unsupported %s operation path %q", label, path)
		}
	}
	return next, nil
}

func requiredOperationValue(operation CurrentMemoryOperation) (string, error) {
	if operation.Value == nil {
		return "", fmt.Errorf(
			"operation %q on %q requires value",
			operation.Op,
			operation.Path,
		)
	}
	value := strings.TrimSpace(*operation.Value)
	if value == "" {
		return "", fmt.Errorf(
			"operation %q on %q requires a non-empty value",
			operation.Op,
			operation.Path,
		)
	}
	return value, nil
}

func applyStringListOperation(
	current []any,
	operation CurrentMemoryOperation,
	label string,
) ([]any, error) {
	values := append([]any{}, current...)
	switch strings.TrimSpace(operation.Op) {
	case "add":
		value, err := requiredOperationValue(operation)
		if err != nil {
			return nil, err
		}
		for _, existing := range values {
			if existing == value {
				return values, nil
			}
		}
		return append(values, value), nil
	case "remove":
		value, err := requiredOperationValue(operation)
		if err != nil {
			return nil, err
		}
		result := make([]any, 0, len(values))
		for _, existing := range values {
			if existing != value {
				result = append(result, existing)
			}
		}
		return result, nil
	case "clear":
		if operation.Value != nil {
			return nil, fmt.Errorf(
				"clear operation on %q must not include value",
				operation.Path,
			)
		}
		return []any{}, nil
	default:
		return nil, fmt.Errorf(
			"%s list path %q only supports add, remove, or clear",
			label,
			operation.Path,
		)
	}
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
	maxChars int,
) CurrentMemoryPreferenceListData {
	items := make([]CurrentMemoryPreferenceItem, 0, len(document.Preferences))
	for _, item := range document.Preferences {
		items = append(items, publicPreferenceItem(item))
	}
	totalSize := int64(len(items))
	return CurrentMemoryPreferenceListData{
		Items:           items,
		TotalSize:       totalSize,
		ETag:            ContentETag(entry.Content),
		UpdatedAt:       formatUpdatedAt(entry.UpdatedAt),
		ProjectionState: BuildPreferenceProjectionState(document, maxChars),
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

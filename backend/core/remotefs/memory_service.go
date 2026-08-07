package remotefs

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"path"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
)

const (
	memoryRootPath       = currentmemory.RootPath
	memoryAgentsPath     = currentmemory.AgentsPath
	memoryUsersPath      = currentmemory.UsersPath
	memorySoulPath       = currentmemory.SoulPath
	memoryProfilePath    = currentmemory.ProfilePath
	memoryPreferencePath = currentmemory.PreferencePath
	memoryReferencesPath = currentmemory.ReferencesPath

	memoryEntryFile = currentmemory.EntryFile
	memoryEntryDir  = currentmemory.EntryDir
)

var (
	defaultSoulYAML       = currentmemory.DefaultSoulYAML
	defaultProfileYAML    = currentmemory.DefaultProfileYAML
	defaultPreferenceYAML = currentmemory.DefaultPreferenceYAML
)

var (
	errMemoryInvalidPath = errors.New("invalid memory path")
	errMemoryNotFound    = errors.New("memory path not found")
	errMemoryConflict    = errors.New("memory path conflict")
	errMemoryProtected   = errors.New("memory mount is protected")
)

type memoryCurrentService struct {
	db                      *gorm.DB
	repository              *currentmemory.Repository
	currentMemory           *currentmemory.Module
	clock                   func() time.Time
	preferenceIndexMaxItems int
}

func newMemoryCurrentService(db *gorm.DB) *memoryCurrentService {
	return newMemoryCurrentServiceWithPreferenceIndexMaxItems(
		db,
		mustPreferenceIndexMaxItems(),
	)
}

func newMemoryCurrentServiceWithPreferenceIndexMaxItems(
	db *gorm.DB,
	maxItems int,
) *memoryCurrentService {
	if maxItems <= 0 {
		panic("preference index max items must be positive")
	}
	return &memoryCurrentService{
		db:                      db,
		repository:              currentmemory.NewRepository(db),
		currentMemory:           currentmemory.NewModuleWithPreferenceIndexMaxItems(db, maxItems),
		clock:                   time.Now,
		preferenceIndexMaxItems: maxItems,
	}
}

func mustPreferenceIndexMaxItems() int {
	value, err := currentmemory.PreferenceIndexMaxItemsFromEnv()
	if err != nil {
		panic(err)
	}
	return value
}

func (s *memoryCurrentService) ensureInitialized(ctx context.Context, userID string) error {
	if s == nil || s.repository == nil {
		return errors.New("memory store db is not configured")
	}
	return s.repository.EnsureInitializedAt(ctx, userID, s.clock().UTC())
}

func defaultMemoryCurrentEntries(userID string, now time.Time) []orm.MemoryCurrentEntry {
	return currentmemory.DefaultEntries(userID, now)
}

func normalizeMemoryCurrentPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return "", errMemoryInvalidPath
	}
	value = strings.Trim(value, "/")
	if value == "" {
		return "", errMemoryInvalidPath
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errMemoryInvalidPath
		}
	}
	cleaned := path.Clean(value)
	if cleaned != memoryRootPath && !strings.HasPrefix(cleaned, memoryRootPath+"/") {
		return "", errMemoryInvalidPath
	}
	return cleaned, nil
}

func isMemoryMountPath(raw string) bool {
	value := strings.Trim(strings.TrimSpace(raw), "/")
	return value == memoryRootPath || strings.HasPrefix(value, memoryRootPath+"/")
}

func (s *memoryCurrentService) list(ctx context.Context, userID, rawPath string) ([]orm.MemoryCurrentEntry, error) {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return nil, err
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return nil, err
	}
	entries, byPath, err := s.loadEntries(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	parent, ok := byPath[entryPath]
	if !ok {
		return nil, errMemoryNotFound
	}
	if parent.EntryType != memoryEntryDir {
		return nil, fmt.Errorf("%w: path is not a directory", errMemoryConflict)
	}
	prefix := entryPath + "/"
	children := make(map[string]orm.MemoryCurrentEntry)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		tail := strings.TrimPrefix(entry.Path, prefix)
		if strings.Contains(tail, "/") {
			continue
		}
		children[entry.Path] = entry
	}
	out := make([]orm.MemoryCurrentEntry, 0, len(children))
	for _, entry := range children {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func (s *memoryCurrentService) info(ctx context.Context, userID, rawPath string) (orm.MemoryCurrentEntry, error) {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	entry, err := s.repository.GetEntry(ctx, userID, entryPath)
	if errors.Is(err, currentmemory.ErrNotFound) {
		return orm.MemoryCurrentEntry{}, errMemoryNotFound
	}
	return entry, err
}

func (s *memoryCurrentService) exists(ctx context.Context, userID, rawPath string) (bool, error) {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return false, err
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return false, err
	}
	return s.repository.EntryExists(ctx, userID, entryPath)
}

func (s *memoryCurrentService) read(ctx context.Context, userID, rawPath string) (orm.MemoryCurrentEntry, error) {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	if entryPath == memorySoulPath || entryPath == memoryProfilePath {
		entry, ensureErr := s.currentMemory.EnsureLatestEntry(ctx, userID, entryPath)
		if errors.Is(ensureErr, currentmemory.ErrNotFound) {
			return orm.MemoryCurrentEntry{}, errMemoryNotFound
		}
		if ensureErr != nil {
			return orm.MemoryCurrentEntry{}, ensureErr
		}
		return entry, nil
	}
	entry, err := s.info(ctx, userID, rawPath)
	if err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	if entry.EntryType != memoryEntryFile {
		return orm.MemoryCurrentEntry{}, fmt.Errorf("%w: path is a directory", errMemoryConflict)
	}
	return entry, nil
}

func (s *memoryCurrentService) write(
	ctx context.Context,
	userID string,
	rawPath string,
	content []byte,
	contentType string,
) (orm.MemoryCurrentEntry, error) {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	if entryPath == memoryRootPath {
		return orm.MemoryCurrentEntry{}, errMemoryProtected
	}
	if err := currentmemory.ValidateDocumentForPath(entryPath, content); err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	nextPreferenceItems := -1
	if entryPath == memoryPreferencePath {
		document, parseErr := currentmemory.ParsePreferences(content)
		if parseErr != nil {
			return orm.MemoryCurrentEntry{}, parseErr
		}
		nextPreferenceItems = len(document.Preferences)
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	var out orm.MemoryCurrentEntry
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, byPath, err := s.loadEntries(ctx, tx, userID)
		if err != nil {
			return err
		}
		if existing, ok := byPath[entryPath]; ok && existing.EntryType == memoryEntryDir {
			return fmt.Errorf("%w: cannot write file over directory", errMemoryConflict)
		}
		if nextPreferenceItems >= 0 {
			currentPreferenceItems := 0
			if existing, ok := byPath[entryPath]; ok {
				document, parseErr := currentmemory.ParsePreferences(existing.Content)
				if parseErr != nil {
					return parseErr
				}
				currentPreferenceItems = len(document.Preferences)
			}
			if capacityErr := currentmemory.ValidatePreferenceCapacity(
				currentPreferenceItems,
				nextPreferenceItems,
				s.preferenceIndexMaxItems,
			); capacityErr != nil {
				return capacityErr
			}
		}
		now := s.clock().UTC()
		if err := s.ensureParentDirectories(ctx, tx, userID, entryPath, byPath, now); err != nil {
			return err
		}
		mimeType, fileType, binary := memoryFileMetadata(entryPath, contentType)
		out = orm.MemoryCurrentEntry{
			UserID:    strings.TrimSpace(userID),
			Path:      entryPath,
			EntryType: memoryEntryFile,
			Content:   append([]byte(nil), content...),
			Size:      int64(len(content)),
			Mime:      mimeType,
			FileType:  fileType,
			Binary:    binary,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return currentmemory.NewRepository(tx).UpsertEntry(ctx, out)
	})
	return out, err
}

func (s *memoryCurrentService) mkdir(ctx context.Context, userID, rawPath string, recursive bool) error {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return err
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, byPath, err := s.loadEntries(ctx, tx, userID)
		if err != nil {
			return err
		}
		if existing, ok := byPath[entryPath]; ok {
			if existing.EntryType != memoryEntryDir {
				return fmt.Errorf("%w: cannot create directory over file", errMemoryConflict)
			}
			return nil
		}
		now := s.clock().UTC()
		if recursive {
			return s.ensureDirectoryPath(ctx, tx, userID, entryPath, byPath, now)
		}
		parentPath := path.Dir(entryPath)
		parent, ok := byPath[parentPath]
		if !ok || parent.EntryType != memoryEntryDir {
			return fmt.Errorf("%w: parent directory not found", errMemoryNotFound)
		}
		entry := memoryDirEntry(userID, entryPath, now)
		return tx.Create(&entry).Error
	})
}

func (s *memoryCurrentService) delete(ctx context.Context, userID, rawPath string, recursive bool) error {
	entryPath, err := normalizeMemoryCurrentPath(rawPath)
	if err != nil {
		return err
	}
	if entryPath == memoryRootPath {
		return errMemoryProtected
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entries, byPath, err := s.loadEntries(ctx, tx, userID)
		if err != nil {
			return err
		}
		target, ok := byPath[entryPath]
		if !ok {
			return errMemoryNotFound
		}
		paths := []string{entryPath}
		if target.EntryType == memoryEntryDir {
			prefix := entryPath + "/"
			for _, entry := range entries {
				if strings.HasPrefix(entry.Path, prefix) {
					paths = append(paths, entry.Path)
				}
			}
			if len(paths) > 1 && !recursive {
				return fmt.Errorf("%w: directory is not empty", errMemoryConflict)
			}
		}
		return currentmemory.NewRepository(tx).DeletePaths(ctx, userID, paths)
	})
}

func (s *memoryCurrentService) copy(
	ctx context.Context,
	userID string,
	rawFrom string,
	rawTo string,
	overwrite bool,
) error {
	if overwrite {
		return fmt.Errorf("%w: overwrite is not supported", errMemoryConflict)
	}
	from, to, err := normalizeMemoryPathPair(rawFrom, rawTo)
	if err != nil {
		return err
	}
	if from == memoryRootPath {
		return errMemoryProtected
	}
	if from == to {
		return nil
	}
	if strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("%w: cannot copy directory into its child", errMemoryInvalidPath)
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.copyEntries(ctx, tx, userID, from, to)
	})
}

func (s *memoryCurrentService) move(ctx context.Context, userID, rawFrom, rawTo string) error {
	from, to, err := normalizeMemoryPathPair(rawFrom, rawTo)
	if err != nil {
		return err
	}
	if from == memoryRootPath || to == memoryRootPath {
		return errMemoryProtected
	}
	if from == to {
		return nil
	}
	if strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("%w: cannot move directory into its child", errMemoryInvalidPath)
	}
	if err := s.ensureInitialized(ctx, userID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.copyEntries(ctx, tx, userID, from, to); err != nil {
			return err
		}
		entries, _, err := s.loadEntries(ctx, tx, userID)
		if err != nil {
			return err
		}
		paths := make([]string, 0)
		for _, entry := range entries {
			if entry.Path == from || strings.HasPrefix(entry.Path, from+"/") {
				paths = append(paths, entry.Path)
			}
		}
		return currentmemory.NewRepository(tx).DeletePaths(ctx, userID, paths)
	})
}

func (s *memoryCurrentService) copyEntries(
	ctx context.Context,
	tx *gorm.DB,
	userID string,
	from string,
	to string,
) error {
	entries, byPath, err := s.loadEntries(ctx, tx, userID)
	if err != nil {
		return err
	}
	if _, ok := byPath[from]; !ok {
		return errMemoryNotFound
	}
	for _, entry := range entries {
		if entry.Path == to || strings.HasPrefix(entry.Path, to+"/") {
			return fmt.Errorf("%w: target already exists", errMemoryConflict)
		}
	}
	now := s.clock().UTC()
	if err := s.ensureParentDirectories(ctx, tx, userID, to, byPath, now); err != nil {
		return err
	}
	source := make([]orm.MemoryCurrentEntry, 0)
	for _, entry := range entries {
		if entry.Path == from || strings.HasPrefix(entry.Path, from+"/") {
			source = append(source, entry)
		}
	}
	sort.Slice(source, func(i, j int) bool { return source[i].Path < source[j].Path })
	clones := make([]orm.MemoryCurrentEntry, 0, len(source))
	for _, entry := range source {
		entry.Path = to + strings.TrimPrefix(entry.Path, from)
		entry.Content = append([]byte(nil), entry.Content...)
		entry.CreatedAt = now
		entry.UpdatedAt = now
		if entry.EntryType == memoryEntryFile {
			if err := currentmemory.ValidateDocumentForPath(entry.Path, entry.Content); err != nil {
				return err
			}
		}
		clones = append(clones, entry)
	}
	return currentmemory.NewRepository(tx).CreateEntries(ctx, clones)
}

func normalizeMemoryPathPair(rawFrom, rawTo string) (string, string, error) {
	from, err := normalizeMemoryCurrentPath(rawFrom)
	if err != nil {
		return "", "", err
	}
	to, err := normalizeMemoryCurrentPath(rawTo)
	if err != nil {
		return "", "", err
	}
	return from, to, nil
}

func (s *memoryCurrentService) loadEntries(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) ([]orm.MemoryCurrentEntry, map[string]orm.MemoryCurrentEntry, error) {
	entries, err := currentmemory.NewRepository(db).ListEntries(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	byPath := make(map[string]orm.MemoryCurrentEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	return entries, byPath, nil
}

func (s *memoryCurrentService) ensureParentDirectories(
	ctx context.Context,
	tx *gorm.DB,
	userID string,
	entryPath string,
	byPath map[string]orm.MemoryCurrentEntry,
	now time.Time,
) error {
	return s.ensureDirectoryPath(ctx, tx, userID, path.Dir(entryPath), byPath, now)
}

func (s *memoryCurrentService) ensureDirectoryPath(
	ctx context.Context,
	tx *gorm.DB,
	userID string,
	dirPath string,
	byPath map[string]orm.MemoryCurrentEntry,
	now time.Time,
) error {
	_ = ctx
	if dirPath == "." || dirPath == "" {
		return errMemoryInvalidPath
	}
	parts := strings.Split(dirPath, "/")
	for i := 1; i <= len(parts); i++ {
		current := strings.Join(parts[:i], "/")
		if existing, ok := byPath[current]; ok {
			if existing.EntryType != memoryEntryDir {
				return fmt.Errorf("%w: parent path is a file", errMemoryConflict)
			}
			continue
		}
		entry := memoryDirEntry(userID, current, now)
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		byPath[current] = entry
	}
	return nil
}

func memoryDirEntry(userID, entryPath string, now time.Time) orm.MemoryCurrentEntry {
	return orm.MemoryCurrentEntry{
		UserID:    strings.TrimSpace(userID),
		Path:      entryPath,
		EntryType: memoryEntryDir,
		FileType:  "directory",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func memoryFileMetadata(entryPath, contentType string) (string, string, bool) {
	mimeType := strings.TrimSpace(contentType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(path.Ext(entryPath)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	}
	fileType := strings.TrimPrefix(strings.ToLower(path.Ext(entryPath)), ".")
	switch fileType {
	case "yml":
		fileType = "yaml"
	case "md", "markdown":
		fileType = "markdown"
	case "":
		fileType = "unknown"
	}
	binary := !(strings.HasPrefix(mediaType, "text/") ||
		mediaType == "application/json" ||
		mediaType == "application/yaml" ||
		mediaType == "application/x-yaml" ||
		strings.HasSuffix(mediaType, "+json") ||
		strings.HasSuffix(mediaType, "+yaml"))
	return mimeType, fileType, binary
}

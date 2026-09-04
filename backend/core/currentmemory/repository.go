package currentmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/maintenance"
)

var (
	ErrNotFound = errors.New("current memory entry not found")
	ErrConflict = errors.New("current memory content conflict")
)

type Repository struct {
	db                   *gorm.DB
	clock                func() time.Time
	beforeCompareAndSwap func()
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db, clock: time.Now}
}

func (r *Repository) EnsureInitialized(ctx context.Context, userID string) error {
	return r.EnsureInitializedAt(ctx, userID, r.now())
}

func (r *Repository) EnsureInitializedAt(
	ctx context.Context,
	userID string,
	now time.Time,
) error {
	const maxSQLiteAttempts = 8

	for attempt := 0; attempt < maxSQLiteAttempts; attempt++ {
		err := r.ensureInitializedOnce(ctx, userID, now)
		if err == nil || !isSQLiteBusy(err) || attempt == maxSQLiteAttempts-1 {
			return err
		}

		timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (r *Repository) ensureInitializedOnce(
	ctx context.Context,
	userID string,
	now time.Time,
) error {
	if r == nil || r.db == nil {
		return errors.New("memory store db is not configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("memory user_id is required")
	}
	return maintenance.UserTransaction(ctx, r.db, userID, func(tx *gorm.DB) error {
		if err := maintenance.Authorize(ctx, tx, userID, false); err != nil {
			return err
		}
		entries := DefaultEntries(userID, now.UTC())
		directories := make([]orm.MemoryCurrentEntry, 0, len(entries))
		files := make([]orm.MemoryCurrentEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.EntryType == EntryDir {
				directories = append(directories, entry)
			} else {
				files = append(files, entry)
			}
		}
		// SQLite's GORM batch encoder turns a nil []byte into an empty BLOB when
		// directory and file rows share one INSERT. The schema deliberately
		// requires directory content to be SQL NULL, so omit the content column
		// for directory rows and insert files separately in the same transaction.
		if len(directories) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Omit("content").Create(&directories).Error; err != nil {
				return err
			}
		}
		if len(files) > 0 {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&files).Error
		}
		return nil
	})
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "sqlite_busy")
}

func DefaultEntries(userID string, now time.Time) []orm.MemoryCurrentEntry {
	yamlEntry := func(entryPath, content string) orm.MemoryCurrentEntry {
		data := []byte(content)
		return orm.MemoryCurrentEntry{
			UserID:    strings.TrimSpace(userID),
			Path:      entryPath,
			EntryType: EntryFile,
			Content:   data,
			Size:      int64(len(data)),
			Mime:      "application/yaml; charset=utf-8",
			FileType:  "yaml",
			Binary:    false,
			CreatedAt: now.UTC(),
			UpdatedAt: now.UTC(),
		}
	}
	dirEntry := func(entryPath string) orm.MemoryCurrentEntry {
		return orm.MemoryCurrentEntry{
			UserID:    strings.TrimSpace(userID),
			Path:      entryPath,
			EntryType: EntryDir,
			FileType:  "directory",
			CreatedAt: now.UTC(),
			UpdatedAt: now.UTC(),
		}
	}
	return []orm.MemoryCurrentEntry{
		dirEntry(RootPath),
		dirEntry(AgentsPath),
		dirEntry(UsersPath),
		dirEntry(ReferencesPath),
		yamlEntry(SoulPath, DefaultSoulYAML),
		yamlEntry(ProfilePath, DefaultProfileYAML),
		yamlEntry(PreferencePath, DefaultPreferenceYAML),
	}
}

func (r *Repository) GetEntry(
	ctx context.Context,
	userID string,
	entryPath string,
) (orm.MemoryCurrentEntry, error) {
	return r.getEntry(ctx, userID, entryPath, false)
}

func (r *Repository) GetEntryForUpdate(
	ctx context.Context,
	userID string,
	entryPath string,
) (orm.MemoryCurrentEntry, error) {
	return r.getEntry(ctx, userID, entryPath, true)
}

func (r *Repository) getEntry(
	ctx context.Context,
	userID string,
	entryPath string,
	forUpdate bool,
) (orm.MemoryCurrentEntry, error) {
	if r == nil || r.db == nil {
		return orm.MemoryCurrentEntry{}, errors.New("memory store db is not configured")
	}
	query := r.db.WithContext(ctx)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var entry orm.MemoryCurrentEntry
	err := query.
		Where("user_id = ? AND path = ?", strings.TrimSpace(userID), entryPath).
		Take(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.MemoryCurrentEntry{}, ErrNotFound
	}
	return entry, err
}

func (r *Repository) ListEntries(
	ctx context.Context,
	userID string,
) ([]orm.MemoryCurrentEntry, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("memory store db is not configured")
	}
	var entries []orm.MemoryCurrentEntry
	err := r.db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("path ASC").
		Find(&entries).Error
	return entries, err
}

func (r *Repository) EntryExists(
	ctx context.Context,
	userID string,
	entryPath string,
) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("memory store db is not configured")
	}
	var count int64
	err := r.db.WithContext(ctx).
		Model(&orm.MemoryCurrentEntry{}).
		Where("user_id = ? AND path = ?", strings.TrimSpace(userID), entryPath).
		Count(&count).Error
	return count == 1, err
}

func (r *Repository) UpsertEntry(
	ctx context.Context,
	entry orm.MemoryCurrentEntry,
) error {
	if r == nil || r.db == nil {
		return errors.New("memory store db is not configured")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "path"}},
		DoUpdates: clause.Assignments(map[string]any{
			"entry_type": entry.EntryType,
			"content":    entry.Content,
			"size":       entry.Size,
			"mime":       entry.Mime,
			"file_type":  entry.FileType,
			"binary":     entry.Binary,
			"updated_at": entry.UpdatedAt,
		}),
	}).Create(&entry).Error
}

func (r *Repository) CreateEntries(
	ctx context.Context,
	entries []orm.MemoryCurrentEntry,
) error {
	if len(entries) == 0 {
		return nil
	}
	if r == nil || r.db == nil {
		return errors.New("memory store db is not configured")
	}
	return r.db.WithContext(ctx).Create(&entries).Error
}

func (r *Repository) DeletePaths(
	ctx context.Context,
	userID string,
	entryPaths []string,
) error {
	if len(entryPaths) == 0 {
		return nil
	}
	if r == nil || r.db == nil {
		return errors.New("memory store db is not configured")
	}
	return r.db.WithContext(ctx).
		Where("user_id = ? AND path IN ?", strings.TrimSpace(userID), entryPaths).
		Delete(&orm.MemoryCurrentEntry{}).Error
}

func (r *Repository) DeletePath(
	ctx context.Context,
	userID string,
	entryPath string,
) error {
	return r.DeletePaths(ctx, userID, []string{entryPath})
}

func (r *Repository) CompareAndSwapFileContent(
	ctx context.Context,
	userID string,
	entryPath string,
	expectedContent []byte,
	content []byte,
	now time.Time,
) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("memory store db is not configured")
	}
	if r.beforeCompareAndSwap != nil {
		r.beforeCompareAndSwap()
	}
	result := r.db.WithContext(ctx).
		Model(&orm.MemoryCurrentEntry{}).
		Where(
			"user_id = ? AND path = ? AND entry_type = ? AND content = ?",
			strings.TrimSpace(userID),
			entryPath,
			EntryFile,
			expectedContent,
		).
		Updates(map[string]any{
			"content":    append([]byte(nil), content...),
			"size":       int64(len(content)),
			"updated_at": now.UTC(),
		})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) UpdateFileContent(
	ctx context.Context,
	userID string,
	entryPath string,
	content []byte,
	now time.Time,
) error {
	if r == nil || r.db == nil {
		return errors.New("memory store db is not configured")
	}
	result := r.db.WithContext(ctx).
		Model(&orm.MemoryCurrentEntry{}).
		Where(
			"user_id = ? AND path = ? AND entry_type = ?",
			strings.TrimSpace(userID),
			entryPath,
			EntryFile,
		).
		Updates(map[string]any{
			"content":    append([]byte(nil), content...),
			"size":       int64(len(content)),
			"updated_at": now.UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) Transaction(
	ctx context.Context,
	fn func(*Repository) error,
) error {
	if r == nil || r.db == nil {
		return errors.New("memory store db is not configured")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		child := &Repository{
			db:                   tx,
			clock:                r.clock,
			beforeCompareAndSwap: r.beforeCompareAndSwap,
		}
		return fn(child)
	})
}

func ContentETag(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Repository) now() time.Time {
	if r != nil && r.clock != nil {
		return r.clock().UTC()
	}
	return time.Now().UTC()
}

func (r *Repository) PreferenceTransaction(ctx context.Context, userID string, fn func(*Repository) error) error {
	return maintenance.UserTransaction(ctx, r.db, userID, func(tx *gorm.DB) error {
		if err := maintenance.Authorize(ctx, tx, userID, true); err != nil {
			return err
		}
		child := *r
		child.db = tx
		return fn(&child)
	})
}

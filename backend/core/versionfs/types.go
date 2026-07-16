package versionfs

import (
	"errors"
	"time"
)

const (
	EntryTypeFile = "file"
	EntryTypeDir  = "dir"

	OverlayUpsert = "upsert"
	OverlayDelete = "delete"

	DecisionPending  = "pending"
	DecisionAccepted = "accepted"
	DecisionRejected = "rejected"
)

var (
	ErrDraftEmpty           = errors.New("versionfs draft is empty")
	ErrDraftConflict        = errors.New("cannot rollback while draft overlay exists")
	ErrStaleDraftVersion    = errors.New("versionfs stale draft version")
	ErrDraftBaseConflict    = errors.New("versionfs draft base revision conflict")
	ErrHeadRevisionConflict = errors.New("versionfs head revision conflict")
)

type Entry struct {
	Path      string
	EntryType string
	BlobHash  string
	Size      int64
	Mime      string
	FileType  string
	Binary    bool
	Mode      int
	FromHead  bool
	FromDraft bool
}

type Overlay struct {
	Path      string
	Op        string
	EntryType string
	BlobHash  string
	Size      int64
	Mime      string
	FileType  string
	Binary    bool
	Mode      int
	UpdatedAt time.Time
}

type DiffLine struct {
	Type                    string
	Text                    string
	HTML                    string
	OldLine                 int
	NewLine                 int
	DisplayNoNewLineWarning bool
	HunkID                  string
	Decision                string
	OldStart                int
	OldLines                int
	NewStart                int
	NewLines                int
}

type ReviewFile struct {
	Path              string
	Type              string
	Status            string
	Binary            bool
	TooLarge          bool
	ReviewID          string
	ReviewVersion     int64
	DraftVersion      int64
	BaseRevisionID    string
	DraftSnapshotHash string
	CanUndo           bool
	HunkCount         int
	PendingCount      int
	AcceptedCount     int
	RejectedCount     int
	DiffLines         []DiffLine
}

type ReviewSessionMeta struct {
	ID                string
	Path              string
	Status            string
	Version           int64
	DraftVersion      int64
	BaseRevisionID    string
	DraftSnapshotHash string
}

type BlobInfo struct {
	Hash     string
	Size     int64
	Mime     string
	FileType string
	Binary   bool
}

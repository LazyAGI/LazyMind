package crawl

import (
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	RunStatusSucceeded = "SUCCEEDED"
	RunStatusCanceled  = "CANCELED"
	RunStatusFailed    = "FAILED"
)

type BindingRunClaim struct {
	RunID             string
	SourceID          string
	BindingID         string
	BindingGeneration int64
	TriggerType       string
	ScopeType         connector.ScopeType
	ScopeRef          connector.ScopeRef
	Cursor            string
}

type Coverage struct {
	ScopeType         connector.ScopeType
	CoveredObjectKeys []string
	CoveredSubtrees   []string
	CoveredTargetRoot bool
	Complete          bool
	Watermark         string
	ExcludedReason    string
}

type Counts struct {
	Seen      int64
	New       int64
	Modified  int64
	Deleted   int64
	Unchanged int64
}

type RunResult struct {
	RunID        string
	Status       string
	Coverage     Coverage
	NextCursor   string
	Counts       Counts
	ErrorCode    string
	ErrorMessage string
}

type CrawlLoopState struct{}

type CrawlRequestKind string

const (
	CrawlRequestKindFetch        CrawlRequestKind = "fetch"
	CrawlRequestKindListChildren CrawlRequestKind = "list_children"
)

type CrawlRequest struct {
	Kind         CrawlRequestKind
	Fetch        connector.FetchPageRequest
	ListChildren connector.ListChildrenRequest
}

type ReduceSeenInput struct {
	SourceID          string
	BindingID         string
	BindingGeneration int64
	RunID             string
	Objects           []store.SourceObject
	DetectedAt        time.Time
}

type ReduceSeenResult struct {
	NewCount       int64
	ModifiedCount  int64
	DeletedCount   int64
	UnchangedCount int64
	States         []store.DocumentState
}

type ReduceMissingInput struct {
	SourceID          string
	BindingID         string
	BindingGeneration int64
	RunID             string
	Coverage          Coverage
	SeenObjectKeys    []string
	RunSucceeded      bool
	DetectedAt        time.Time
}

type ReduceMissingResult struct {
	DeletedCount       int64
	AffectedObjectKeys []string
}

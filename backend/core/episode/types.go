package episode

import "errors"

const (
	DefaultCandidateLimit = 20
	MaxCandidateLimit     = 100
	DefaultRecentLimit    = 3
	MaxRecentLimit        = 100
	DefaultPageSize       = 20
	MaxPageSize           = 100

	SourceKindChatExplicit = "chat_explicit"
	SourceKindMemoryReview = "memory_review"

	EpisodeTypeDecision = "decision"
	EpisodeTypeProgress = "progress"
	EpisodeTypeResult   = "result"
	EpisodeTypeBlocker  = "blocker"
	EpisodeTypeEvent    = "event"

	CreateStatusCreated    = "created"
	CreateStatusIdempotent = "idempotent"
	DeleteStatusDeleted    = "deleted"
	DeleteStatusNotFound   = "not_found"
)

var ErrNotFound = errors.New("episode not found")

type CreateInput struct {
	UserID           string `json:"user_id"`
	ConversationID   string `json:"conversation_id"`
	SourceKind       string `json:"source_kind"`
	EpisodeType      string `json:"episode_type"`
	Summary          string `json:"summary"`
	SearchText       string `json:"search_text"`
	TokenizerVersion string `json:"tokenizer_version"`
	OccurredAtMS     int64  `json:"occurred_at_ms"`
}

type CreateResult struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}

type DeleteResult struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}

type Episode struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	ConversationID string `json:"conversation_id"`
	SourceKind     string `json:"source_kind"`
	EpisodeType    string `json:"episode_type"`
	Summary        string `json:"summary"`
	OccurredAtMS   int64  `json:"occurred_at_ms"`
	RecordedAtMS   int64  `json:"recorded_at_ms"`
	HitCount       int64  `json:"hit_count"`
}

type PublicEpisode struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	SourceKind     string `json:"source_kind"`
	EpisodeType    string `json:"episode_type"`
	Summary        string `json:"summary"`
	OccurredAtMS   int64  `json:"occurred_at_ms"`
	RecordedAtMS   int64  `json:"recorded_at_ms"`
	HitCount       int64  `json:"hit_count"`
}

type SearchCandidate struct {
	Episode      Episode `json:"episode"`
	LexicalScore float64 `json:"lexical_score"`
}

type Page struct {
	Items         []Episode `json:"items"`
	TotalSize     int64     `json:"total_size"`
	NextPageToken string    `json:"next_page_token"`
}

type PublicPage struct {
	Items         []PublicEpisode `json:"items"`
	TotalSize     int64           `json:"total_size"`
	NextPageToken string          `json:"next_page_token"`
}

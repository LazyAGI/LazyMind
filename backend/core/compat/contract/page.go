package contract

const (
	DefaultPageSize = 20
	MinPageSize     = 1
	MaxPageSize     = 100
)

// PageRequest carries opaque-token pagination input.
type PageRequest struct {
	PageSize  int
	PageToken string
}

// PageResult carries opaque-token pagination output.
type PageResult struct {
	NextPageToken string
	Total         *int64
}

// Normalize clamps PageSize and preserves PageToken unchanged.
func (p PageRequest) Normalize() PageRequest {
	if p.PageSize < MinPageSize {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	return p
}

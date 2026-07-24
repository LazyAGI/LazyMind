package episode

import (
	"context"
	"fmt"

	"lazymind/core/common/orm"
)

type lexicalHit struct {
	Row   orm.EpisodeMemory
	Score float64
}

// SearchAdapter isolates dialect-specific lexical search while Repository owns
// identity, tenancy, pagination, deletion, and hit accounting.
type SearchAdapter interface {
	Search(ctx context.Context, userID string, terms []string, limit int) ([]lexicalHit, error)
}

type unavailableSearchAdapter struct {
	dialect string
}

func (a unavailableSearchAdapter) Search(
	context.Context,
	string,
	[]string,
	int,
) ([]lexicalHit, error) {
	return nil, fmt.Errorf("episode search does not support %q", a.dialect)
}

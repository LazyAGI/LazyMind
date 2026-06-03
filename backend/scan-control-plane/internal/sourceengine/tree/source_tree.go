package tree

type DBSourceTreeQueryEngine struct {
	repo   SourceTreeReadRepository
	limits TreeQueryLimits
}

func NewDBSourceTreeQueryEngine(repo SourceTreeReadRepository, limits TreeQueryLimits) *DBSourceTreeQueryEngine {
	return &DBSourceTreeQueryEngine{repo: repo, limits: defaultLimits(limits)}
}

package evalset

const (
	StatusActive    = "active"
	StatusImporting = "importing"
	StatusFailed    = "failed"

	ShardStatusOpen   = "open"
	ShardStatusSealed = "sealed"

	SourceUpload   = "upload"
	SourceManual   = "manual"
	SourceFlowback = "flowback"

	DefaultShardID                = "eval_shard_0001"
	DefaultShardRowLimit          = int64(200000)
	DefaultShardRowOpenThreshold  = int64(120000)
	DefaultShardSizeLimitBytes    = int64(8 * 1024 * 1024 * 1024)
	DefaultShardSizeOpenThreshold = int64(5 * 1024 * 1024 * 1024)
)

package evalset

import (
	"fmt"
	"strings"

	"lazymind/core/common"
)

func newEvalSetID() string {
	return "eval_set_" + common.GenerateID()
}

func newShardID() string {
	return "eval_shard_" + common.GenerateID()
}

func newEvalSetItemID() string {
	return "eval_item_" + common.GenerateID()
}

func partitionTableNameForShard(shardID string) string {
	var b strings.Builder
	b.WriteString("eval_set_items_p_")
	for _, r := range shardID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

func quoteSQLString(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func createPartitionSQL(shardID string) string {
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS public.%s PARTITION OF public.eval_set_items FOR VALUES IN (%s)",
		partitionTableNameForShard(shardID),
		quoteSQLString(shardID),
	)
}

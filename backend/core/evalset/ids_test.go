package evalset

import (
	"strings"
	"testing"
)

// TestNewEvalSetID generates IDs with the eval_set_ prefix.
func TestNewEvalSetID(t *testing.T) {
	id := newEvalSetID()
	if !strings.HasPrefix(id, "eval_set_") {
		t.Fatalf("got %q, want prefix eval_set_", id)
	}
	// Consecutive calls produce unique IDs
	id2 := newEvalSetID()
	if id == id2 {
		t.Fatal("expected different IDs")
	}
}

// TestNewShardID generates IDs with the eval_shard_ prefix.
func TestNewShardID(t *testing.T) {
	id := newShardID()
	if !strings.HasPrefix(id, "eval_shard_") {
		t.Fatalf("got %q, want prefix eval_shard_", id)
	}
}

// TestNewEvalSetItemID generates IDs with the eval_item_ prefix.
func TestNewEvalSetItemID(t *testing.T) {
	id := newEvalSetItemID()
	if !strings.HasPrefix(id, "eval_item_") {
		t.Fatalf("got %q, want prefix eval_item_", id)
	}
}

// TestPartitionTableNameForShard replaces non-alphanumeric chars with underscore.
func TestPartitionTableNameForShard(t *testing.T) {
	tests := []struct {
		shardID string
		want    string
	}{
		{"eval_shard_0001", "eval_set_items_p_eval_shard_0001"},
		{"shard-001", "eval_set_items_p_shard_001"},
		{"a.b/c:d", "eval_set_items_p_a_b_c_d"},
		{"", "eval_set_items_p_"},
	}
	for _, tt := range tests {
		t.Run(tt.shardID, func(t *testing.T) {
			got := partitionTableNameForShard(tt.shardID)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestQuoteSQLString wraps value in single quotes and escapes internal quotes.
func TestQuoteSQLString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"it's", "'it''s'"},
		{"", "''"},
		{"a'b'c", "'a''b''c'"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := quoteSQLString(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCreatePartitionSQL generates valid CREATE TABLE ... PARTITION OF SQL.
func TestCreatePartitionSQL(t *testing.T) {
	sql := createPartitionSQL("eval_shard_0001")
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS") {
		t.Fatal("missing CREATE TABLE IF NOT EXISTS")
	}
	if !strings.Contains(sql, "PARTITION OF") {
		t.Fatal("missing PARTITION OF")
	}
	if !strings.Contains(sql, "eval_shard_0001") {
		t.Fatal("missing shard ID in SQL")
	}
}

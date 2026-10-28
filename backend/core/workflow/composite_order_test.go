package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func TestCompositeOrderAtomicPermutation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, id := range []string{"prompts", "slides"} {
		if err := db.Create(&orm.WorkflowSlotOrder{SessionID: "linked", SlotID: id, OrderList: json.RawMessage(`[8, 2, 5]`), OrderVersion: 3}).Error; err != nil {
			t.Fatal(err)
		}
	}
	changes := []SlotOrderChange{{SlotID: "prompts", Order: []int{5, 8, 2}, Version: 3}, {SlotID: "slides", Order: []int{5, 8, 2}, Version: 2}}
	if err := ReorderSlots(ctx, db.DB, "linked", changes); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	first, err := GetSlotOrder(ctx, db.DB, "linked", "prompts")
	assertCompositeSlotOrder(t, first, err, []int{8, 2, 5}, 3)
	for _, order := range [][]int{{8, 8, 5}, {8, 2}, {8, 2, 99}} {
		if err := ReorderSlot(ctx, db.DB, "linked", "prompts", order, 3); !errors.Is(err, ErrInvalidOrder) {
			t.Fatalf("accepted invalid order %v: %v", order, err)
		}
	}
	changes[1].Version = 3
	if err := ReorderSlots(ctx, db.DB, "linked", changes); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"prompts", "slides"} {
		row, err := GetSlotOrder(ctx, db.DB, "linked", id)
		assertCompositeSlotOrder(t, row, err, []int{5, 8, 2}, 4)
	}
}

func TestCompositeOrderHTTPRejectsPartialCommit(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.Create(&orm.WorkflowSession{ID: "linked-http", ConversationID: "conversation", Status: SessionStatusCompleted}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"prompts", "slides"} {
		if err := db.Create(&orm.WorkflowSlotOrder{SessionID: "linked-http", SlotID: id, OrderList: json.RawMessage(`[8, 2]`), OrderVersion: 3}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		version int
		status  int
	}{{2, 409}, {3, 200}} {
		request := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(fmt.Sprintf(`{"order":[2,8],"version":3,"linked_orders":[{"slot_id":"slides","order":[2,8],"version":%d}]}`, tc.version)))
		request = mux.SetURLVars(request, map[string]string{"session_id": "linked-http", "slot_id": "prompts"})
		response := httptest.NewRecorder()
		ReorderSlotItems(response, request)
		if response.Code != tc.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		want, wantVersion := []int{8, 2}, 3
		if tc.status == 200 {
			want, wantVersion = []int{2, 8}, 4
		}
		for _, slot := range []string{"prompts", "slides"} {
			row, err := GetSlotOrder(context.Background(), db.DB, "linked-http", slot)
			assertCompositeSlotOrder(t, row, err, want, wantVersion)
		}
	}
}

func assertCompositeSlotOrder(t *testing.T, row *orm.WorkflowSlotOrder, err error, want []int, version int) {
	t.Helper()
	if err != nil || row == nil {
		t.Fatalf("read slot order: row=%+v err=%v", row, err)
	}
	// PostgreSQL JSONB may add whitespace; compare ordered values, not JSON bytes.
	var order []int
	if err := json.Unmarshal(row.OrderList, &order); err != nil {
		t.Fatalf("decode slot %s order: %v", row.SlotID, err)
	}
	if !slices.Equal(order, want) || row.OrderVersion != version {
		t.Fatalf("slot %s: order=%v version=%d; want order=%v version=%d",
			row.SlotID, order, row.OrderVersion, want, version)
	}
}

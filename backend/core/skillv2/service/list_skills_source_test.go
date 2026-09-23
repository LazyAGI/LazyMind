package service

import (
	"context"
	"reflect"
	"testing"
)

func TestListSkillsSourceFiltersBeforePagination(t *testing.T) {
	db := newSkillV2TestDB(t)
	for i, row := range []struct{ id, category, origin string }{
		{"builtin-legacy", "research", "builtin-uid"},
		{"builtin-internal", "internal", "builtin-uid-2"},
		{"builtin-external", "external", "builtin-uid-3"},
		{"ordinary-internal", "internal", ""},
		{"ordinary-external", "external", ""},
	} {
		seedSkillWithHeadRevision(t, db, row.id, "rev-"+row.id)
		if err := db.Model(&skillRow{}).Where("id = ?", row.id).Updates(map[string]any{
			"category": row.category, "origin_builtin_skill_uid": row.origin, "sort_rank": 100 - i,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := newListSkillService(t, db)
	for _, tc := range []struct {
		source string
		offset int
		wantID string
		total  int64
	}{
		{"builtin", 1, "builtin-internal", 3},
		{"internal", 0, "ordinary-internal", 1},
		{"external", 0, "ordinary-external", 1},
	} {
		t.Run(tc.source, func(t *testing.T) {
			got, err := svc.ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001", Source: tc.source, Offset: tc.offset, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if got.Total != tc.total || !reflect.DeepEqual(itemIDs(got.Items), []string{tc.wantID}) {
				t.Fatalf("got total=%d ids=%v, want total=%d id=%s", got.Total, itemIDs(got.Items), tc.total, tc.wantID)
			}
		})
	}
	got, err := svc.GetSkill(context.Background(), GetSkillRequest{UserID: "user_001", SkillID: "builtin-legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginBuiltinSkillUID != "builtin-uid" || got.Category != "research" {
		t.Fatalf("detail origin=%q category=%q", got.OriginBuiltinSkillUID, got.Category)
	}
}

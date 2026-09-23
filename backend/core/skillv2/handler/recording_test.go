package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestRecordingFrameValidation(t *testing.T) {
	jpeg := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0})
	good := []algo.RecordingFrame{{Image: jpeg, Seconds: 0}, {Image: jpeg, Seconds: 1}}
	if err := validateRecordingFrames(good); err != nil {
		t.Fatal(err)
	}
	for _, frames := range [][]algo.RecordingFrame{good[:1], {{Image: jpeg, Seconds: 1}, {Image: jpeg, Seconds: 0}}, {{Image: "data:image/jpeg;base64,YQ==", Seconds: 0}, good[1]}} {
		if validateRecordingFrames(frames) == nil {
			t.Fatal("accepted invalid recording")
		}
	}
}

func TestRecordingGenerationAndDecisions(t *testing.T) {
	for _, keep := range []bool{true, false} {
		t.Run(map[bool]string{true: "keep", false: "discard"}[keep], func(t *testing.T) {
			db := testutil.NewTestDB(t)
			if err := db.AutoMigrate(&orm.SkillRecording{}); err != nil {
				t.Fatal(err)
			}
			oldDB := store.DB()
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(oldDB, nil, nil) })
			oldGenerate := recordingGenerate
			t.Cleanup(func() { recordingGenerate = oldGenerate })
			recordingGenerate = func(_ context.Context, _ []algo.RecordingFrame, _ string, evidence algo.RecordingEvidence, _ map[string]any) (algo.RecordingSkillResult, error) {
				if len(evidence.Events) != 1 || !strings.Contains(string(evidence.Events[0]), "click") {
					t.Fatalf("missing operation evidence: %+v", evidence)
				}
				return algo.RecordingSkillResult{Name: "录制测试", Description: "有证据的操作", Content: "# 步骤\n1. 输入参数。\n2. 检查输出。"}, nil
			}
			row := orm.SkillRecording{ID: "record-1", UserID: "user_001", ConversationID: "conv-1", Status: "generating", Frames: "sensitive-source", Evidence: `{"events":[{"kind":"click","seconds":1}],"limitations":[]}`, Notes: "private", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			generateRecordedSkill(db.DB, row, "user", nil, nil)
			if err := db.First(&row, "id = ?", row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if row.Status != "pending" || row.SkillID == "" || row.Frames != "[]" || row.Evidence != "{}" || row.Notes != "" {
				t.Fatalf("unexpected generated card: %+v", row)
			}
			var skill orm.SkillV2Skill
			if err := db.First(&skill, "id = ?", row.SkillID).Error; err != nil {
				t.Fatal(err)
			}
			if skill.IsEnabled {
				t.Fatal("generated skill enabled before confirmation")
			}
			enabled := true
			if _, err := newSkillService(db.DB).PatchSkill(context.Background(), skillservice.PatchSkillRequest{SkillID: skill.ID, UserID: "user_001", IsEnabled: &enabled}); err == nil {
				t.Fatal("pending skill could be enabled")
			}
			tags := []string{}
			if _, err := newSkillService(db.DB).PatchSkill(context.Background(), skillservice.PatchSkillRequest{SkillID: skill.ID, UserID: "user_001", Tags: &tags}); err == nil {
				t.Fatal("pending tag could be removed outside decision")
			}
			body, _ := json.Marshal(map[string]any{"id": row.ID, "keep": keep})
			// A different account cannot decide this card.
			request := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
			request.Header.Set("X-User-Id", "other")
			DecideSkillRecording(httptest.NewRecorder(), request)
			var unchanged orm.SkillRecording
			db.First(&unchanged, "id = ?", row.ID)
			if unchanged.Status != "pending" {
				t.Fatal("other user changed recording")
			}
			for i := 0; i < 2; i++ {
				request = httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
				request.Header.Set("X-User-Id", "user_001")
				response := httptest.NewRecorder()
				DecideSkillRecording(response, request)
				if !strings.Contains(response.Body.String(), `"ok":true`) {
					t.Fatalf("decision failed: %s", response.Body.String())
				}
			}
			db.First(&row, "id = ?", row.ID)
			if keep {
				db.First(&skill, "id = ?", row.SkillID)
				if row.Status != "kept" || skill.IsEnabled || strings.Contains(string(skill.Tags), recordingPendingTag) {
					t.Fatalf("invalid kept state: %+v %+v", row, skill)
				}
			} else {
				var count int64
				db.Model(&orm.SkillV2Skill{}).Where("id = ?", row.SkillID).Count(&count)
				if row.Status != "discarded" || count != 0 {
					t.Fatal("discard did not delete skill")
				}
			}
		})
	}
}

func TestRecordingInsufficientEvidenceCreatesNoSkill(t *testing.T) {
	db := testutil.NewTestDB(t)
	db.AutoMigrate(&orm.SkillRecording{})
	old := recordingGenerate
	t.Cleanup(func() { recordingGenerate = old })
	recordingGenerate = func(context.Context, []algo.RecordingFrame, string, algo.RecordingEvidence, map[string]any) (algo.RecordingSkillResult, error) {
		return algo.RecordingSkillResult{Missing: []string{"请补充输出"}}, nil
	}
	row := orm.SkillRecording{ID: "missing", UserID: "user_001", Status: "generating", Frames: "source", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	db.Create(&row)
	generateRecordedSkill(db.DB, row, "", nil, nil)
	db.First(&row, "id = ?", row.ID)
	var count int64
	db.Model(&orm.SkillV2Skill{}).Count(&count)
	if row.Status != "needs_input" || row.Error != "请补充输出" || count != 0 {
		t.Fatalf("unexpected state %+v", row)
	}
}

func TestRecordingStaleAttemptCannotPublish(t *testing.T) {
	db := testutil.NewTestDB(t)
	db.AutoMigrate(&orm.SkillRecording{})
	old := recordingGenerate
	t.Cleanup(func() { recordingGenerate = old })
	recordingGenerate = func(context.Context, []algo.RecordingFrame, string, algo.RecordingEvidence, map[string]any) (algo.RecordingSkillResult, error) {
		return algo.RecordingSkillResult{Name: "旧结果", Description: "旧任务", Content: "# Steps\n1. Old"}, nil
	}
	row := orm.SkillRecording{ID: "retry", UserID: "user_001", Status: "generating", Attempt: 1, Frames: "source", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	db.Create(&row)
	stale := row
	stale.Attempt = 0
	generateRecordedSkill(db.DB, stale, "", nil, nil)
	db.First(&row, "id = ?", row.ID)
	var count int64
	db.Model(&orm.SkillV2Skill{}).Count(&count)
	if row.Status != "generating" || row.Attempt != 1 || count != 0 {
		t.Fatalf("stale worker changed current attempt: %+v", row)
	}
}

func TestRecordingVisionConfigurationFailureKeepsRetryableCard(t *testing.T) {
	db := testutil.NewTestDB(t)
	db.AutoMigrate(&orm.SkillRecording{})
	old := recordingGenerate
	t.Cleanup(func() { recordingGenerate = old })
	reason := "未配置视觉模型，且未能确认主模型能正确读取图片。请配置视觉模型后重试。"
	recordingGenerate = func(context.Context, []algo.RecordingFrame, string, algo.RecordingEvidence, map[string]any) (algo.RecordingSkillResult, error) {
		return algo.RecordingSkillResult{Error: reason}, nil
	}
	row := orm.SkillRecording{ID: "no-vision", UserID: "user_001", Status: "generating", Frames: "source", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	db.Create(&row)
	generateRecordedSkill(db.DB, row, "", nil, nil)
	db.First(&row, "id = ?", row.ID)
	var count int64
	db.Model(&orm.SkillV2Skill{}).Count(&count)
	if row.Status != "failed" || row.Error != reason || row.Frames != "source" || count != 0 {
		t.Fatalf("expected a retryable failure card with no generated skill: %+v", row)
	}
}

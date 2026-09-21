package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/browser"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	skillservice "lazymind/core/skillv2/service"
)

const recordingPendingTag = "recording:pending"

var recordingGenerate = algo.GenerateRecordingSkill

func ListSkillRecordings(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	uid, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	// Interrupted workers must not leave an unretryable card after a server restart.
	if err := db.Model(&orm.SkillRecording{}).Where("user_id = ? AND status = ? AND updated_at < ?", uid, "generating", time.Now().Add(-12*time.Minute)).Updates(map[string]any{"status": "failed", "error": "Generation interrupted. Please retry.", "updated_at": time.Now()}).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	var rows []orm.SkillRecording
	q := db.Where("user_id = ?", uid)
	if cid := r.URL.Query().Get("conversation_id"); cid != "" {
		q = q.Where("conversation_id = ?", cid)
	}
	if sid := r.URL.Query().Get("skill_id"); sid != "" {
		q = q.Where("skill_id = ?", sid)
	}
	if err := q.Order("created_at ASC").Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}

	for i := range rows {
		row := &rows[i]
		if row.Status != "pending" && row.Status != "kept" && row.Status != "discarded" {
			continue
		}
		var skill orm.SkillV2Skill
		err := db.Select("skill_name", "description", "tags").Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", row.SkillID, uid).First(&skill).Error
		if err == gorm.ErrRecordNotFound {
			row.Status = "discarded"
			if err := db.Model(&orm.SkillRecording{}).Where("id = ? AND status IN ?", row.ID, []string{"pending", "kept"}).Updates(map[string]any{"status": "discarded", "updated_at": time.Now()}).Error; err != nil {
				replyServiceError(w, err)
				return
			}
		} else if err != nil {
			replyServiceError(w, err)
			return
		} else {
			if row.Status == "discarded" {
				row.Status = "kept"
				for _, tag := range decodeTags(skill.Tags) {
					if tag == recordingPendingTag {
						row.Status = "pending"
					}
				}
				if err := db.Model(&orm.SkillRecording{}).Where("id = ? AND status = ?", row.ID, "discarded").Updates(map[string]any{"status": row.Status, "updated_at": time.Now()}).Error; err != nil {
					replyServiceError(w, err)
					return
				}
			}
			row.Name = skill.SkillName
			row.Description = skill.Description
		}
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}

func validateRecordingFrames(frames []algo.RecordingFrame) error {
	if len(frames) < 2 || len(frames) > 120 {
		return fmt.Errorf("recording requires 2–120 frames")
	}
	last := -1.0
	for _, f := range frames {
		if f.Seconds < 0 || f.Seconds <= last || f.Seconds > 600 {
			return fmt.Errorf("invalid frame timestamp")
		}
		last = f.Seconds
		if !strings.HasPrefix(f.Image, "data:image/jpeg;base64,") || len(f.Image) > 400000 {
			return fmt.Errorf("invalid recording image")
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(f.Image, "data:image/jpeg;base64,"))
		if err != nil || len(b) < 3 || b[0] != 0xff || b[1] != 0xd8 || b[2] != 0xff {
			return fmt.Errorf("invalid JPEG frame")
		}
	}
	return nil
}

func SubmitSkillRecording(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	uid, uname, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Evidence       algo.RecordingEvidence `json:"evidence"`
		ID             string                 `json:"id"`
		ConversationID string                 `json:"conversation_id"`
		Frames         []algo.RecordingFrame  `json:"frames"`
		Notes          string                 `json:"notes"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 24<<20)
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Notes) > 8000 {
		replyError(w, "notes too long", 400)
		return
	}
	var row orm.SkillRecording
	if err := validateRecordingFrames(req.Frames); req.ID == "" && err != nil {
		replyError(w, err.Error(), 400)
		return
	}
	if req.ID != "" {
		if err := db.Where("id = ? AND user_id = ?", req.ID, uid).First(&row).Error; err != nil {
			replyError(w, "recording not found", 404)
			return
		}
		if row.Status != "failed" && row.Status != "needs_input" {
			replyError(w, "recording cannot be retried", 409)
			return
		}
		if row.Evidence != "" {
			if err := json.Unmarshal([]byte(row.Evidence), &req.Evidence); err != nil {
				replyError(w, "recording evidence unavailable", 400)
				return
			}
		}
		if err := json.Unmarshal([]byte(row.Frames), &req.Frames); err != nil {
			replyError(w, "recording source unavailable", 400)
			return
		}
	} else if req.ConversationID != "" {
		var count int64
		if err := db.Model(&orm.Conversation{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", req.ConversationID, uid).Count(&count).Error; err != nil || count != 1 {
			replyError(w, "conversation not found", 404)
			return
		}
	}
	if err := validateRecordingFrames(req.Frames); err != nil {
		replyError(w, err.Error(), 400)
		return
	}
	evidenceJSON, err := json.Marshal(req.Evidence)
	if err != nil || len(evidenceJSON) > 2<<20 || len(req.Evidence.Events) > 1500 || len(req.Evidence.Limitations) > 20 {
		replyError(w, "recording evidence too large", 400)
		return
	}
	for _, event := range req.Evidence.Events {
		var data struct {
			Seconds float64 `json:"seconds"`
			Kind    string  `json:"kind"`
		}
		if json.Unmarshal(event, &data) != nil || data.Seconds < 0 || data.Seconds > 600 || (data.Kind != "click" && data.Kind != "keydown" && data.Kind != "input" && data.Kind != "dom" && data.Kind != "keyup" && data.Kind != "mousedown" && data.Kind != "mouseup" && data.Kind != "mousemove" && data.Kind != "wheel") {
			replyError(w, "invalid recording event", 400)
			return
		}
	}
	cfg, err := modelconfig.LoadLLMConfig(r.Context(), db, uid)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if req.ID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			replyServiceError(w, err)
			return
		}
		raw, _ := json.Marshal(req.Frames)
		row = orm.SkillRecording{ID: hex.EncodeToString(b), UserID: uid, ConversationID: req.ConversationID, Status: "generating", Evidence: string(evidenceJSON), Frames: string(raw), Notes: req.Notes, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		err := db.Transaction(func(tx *gorm.DB) error {
			if row.ConversationID == "" {
				row.ConversationID = row.ID
				conv := orm.Conversation{ID: row.ID, DisplayName: "录制技能", BaseModel: orm.BaseModel{CreateUserID: uid, CreateUserName: uname, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
				if err := tx.Create(&conv).Error; err != nil {
					return err
				}
			}
			return tx.Create(&row).Error
		})
		if err != nil {
			replyServiceError(w, err)
			return
		}
	} else {
		result := db.Model(&orm.SkillRecording{}).Where("id = ? AND user_id = ? AND status IN ?", row.ID, uid, []string{"failed", "needs_input"}).Updates(map[string]any{"status": "generating", "attempt": gorm.Expr("attempt + 1"), "error": "", "notes": req.Notes, "updated_at": time.Now()})
		if result.Error != nil {
			replyServiceError(w, result.Error)
			return
		}
		if result.RowsAffected != 1 {
			replyError(w, "recording already running", 409)
			return
		}
		row.Attempt++
		row.Status = "generating"
		row.Error = ""
		row.Notes = req.Notes
	}
	go generateRecordedSkill(db, row, uname, req.Frames, cfg)
	common.ReplyOK(w, row)
}

func generateRecordedSkill(db *gorm.DB, row orm.SkillRecording, uname string, frames []algo.RecordingFrame, cfg map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	fail := func(status, reason string) {
		db.Model(&orm.SkillRecording{}).Where("id = ? AND status = ? AND attempt = ?", row.ID, "generating", row.Attempt).Updates(map[string]any{"status": status, "error": reason, "updated_at": time.Now()})
	}
	defer func() {
		if recover() != nil {
			fail("failed", "Generation failed. Please retry.")
		}
	}()
	var evidence algo.RecordingEvidence
	if row.Evidence != "" {
		if err := json.Unmarshal([]byte(row.Evidence), &evidence); err != nil {
			fail("failed", "Invalid recording evidence")
			return
		}
	}
	result, err := recordingGenerate(ctx, frames, row.Notes, evidence, cfg)
	if err != nil {
		fail("failed", "Unable to analyze the recording. Check the vision model configuration and retry.")
		return
	}
	if result.Error != "" {
		fail("failed", result.Error)
		return
	}
	if len(result.Missing) > 0 {
		fail("needs_input", strings.Join(result.Missing, "\n"))
		return
	}
	if strings.TrimSpace(result.Name) == "" || strings.TrimSpace(result.Description) == "" || strings.TrimSpace(result.Content) == "" {
		fail("needs_input", "Please describe the purpose, inputs and expected output, or record the missing steps.")
		return
	}
	source, cleanup, err := createSkillSourceFromRequest(ctx, result.Name, "internal", result.Description, result.Content, nil, skillSourceRequest{})
	if err != nil {
		fail("failed", "Invalid generated skill. Please retry.")
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Claim the still-running card before creating its skill; stale workers cannot publish.
		changed := tx.Model(&orm.SkillRecording{}).Where("id = ? AND status = ? AND attempt = ?", row.ID, "generating", row.Attempt).Updates(map[string]any{"status": "pending", "updated_at": time.Now()})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return fmt.Errorf("recording no longer running")
		}
		enabled := false
		skill, err := newSkillService(tx).CreateSkill(ctx, skillservice.CreateSkillRequest{OwnerUserID: row.UserID, OwnerUserName: uname, CreateUserID: row.UserID, CreateUserName: uname, Name: result.Name, Category: "internal", Description: result.Description, Tags: []string{recordingPendingTag}, IsEnabled: &enabled, Source: source})
		if err != nil {
			return err
		}
		return tx.Model(&orm.SkillRecording{}).Where("id = ?", row.ID).Updates(map[string]any{"skill_id": skill.SkillID, "name": result.Name, "description": result.Description, "frames": "[]", "evidence": "{}", "notes": "", "error": ""}).Error
	})
	if err != nil {
		fail("failed", "Unable to save the skill. Check whether its name already exists, then retry with a different name.")
	}
}

func DecideSkillRecording(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	uid, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		ID   string `json:"id"`
		Keep bool   `json:"keep"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var row orm.SkillRecording
		if err := tx.Where("id = ? AND user_id = ?", req.ID, uid).First(&row).Error; err != nil {
			return err
		}
		target := "discarded"
		if req.Keep {
			target = "kept"
		}
		if row.Status == target {
			return nil
		}
		if row.Status != "pending" {
			return fmt.Errorf("recording is not pending")
		}
		res := tx.Model(&orm.SkillRecording{}).Where("id = ? AND status = ?", row.ID, "pending").Updates(map[string]any{"status": target, "updated_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("recording already decided")
		}
		if !req.Keep {
			if err := newSkillService(tx).TrashSkill(r.Context(), skillservice.DeleteSkillRequest{SkillID: row.SkillID, UserID: uid}); err != nil {
				return err
			}
			return newSkillService(tx).PurgeSkill(r.Context(), skillservice.PurgeSkillRequest{SkillID: row.SkillID, UserID: uid})
		}
		var skill orm.SkillV2Skill
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", row.SkillID, uid).First(&skill).Error; err != nil {
			return err
		}
		tags := []string{}
		for _, tag := range decodeTags(skill.Tags) {
			if tag != recordingPendingTag {
				tags = append(tags, tag)
			}
		}
		raw, _ := json.Marshal(tags)
		return tx.Model(&skill).Updates(map[string]any{"tags": raw, "is_enabled": false, "updated_at": time.Now()}).Error
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"ok": true})
}

// RecordingSkillSetup installs the reusable recording guide once per account.
func RecordingSkillSetup(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	uid, uname, ok := requireUser(w, r)
	if !ok {
		return
	}
	const name = "screen-recording-to-skill"
	var existing orm.SkillV2Skill
	err := db.Where("owner_user_id = ? AND skill_name = ? AND category = ? AND deleted_at IS NULL", uid, name, "internal").First(&existing).Error
	if err == nil {
		common.ReplyOK(w, map[string]any{"installed": true, "skill_id": existing.ID})
		return
	}
	if err != gorm.ErrRecordNotFound {
		replyServiceError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		common.ReplyOK(w, map[string]any{"installed": false})
		return
	}
	const description = "将用户明确授权的屏幕操作录制整理为待确认技能。"
	const content = "# 录屏转 Skill\n\n仅在用户主动请求时，引导用户使用对话输入框 + → 录制技能。\n\n1. 提醒用户隐藏账号、密钥和个人信息。\n2. 由用户选择屏幕或窗口，开始录制完整操作。\n3. 结束后，根据按时间排列的画面和用户补充识别用途、输入、步骤及输出。\n4. 信息不足时询问用户，不得编造步骤；敏感值使用占位符。\n5. 生成结果进入我的技能，保持待确认和关闭状态。\n6. 用户在详情页查看、修改并保存，然后选择保留或不保留。保留后由用户自行启用。\n"
	source, cleanup, err := createSkillSourceFromRequest(r.Context(), name, "internal", description, content, nil, skillSourceRequest{})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	enabled := false
	skill, err := newSkillService(db).CreateSkill(r.Context(), skillservice.CreateSkillRequest{OwnerUserID: uid, OwnerUserName: uname, CreateUserID: uid, CreateUserName: uname, Name: name, Category: "internal", Description: description, Source: source, IsEnabled: &enabled})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"installed": true, "skill_id": skill.SkillID})
}

// RecordingBrowser exposes only bounded recording commands, never arbitrary CDP or scripts.
func RecordingBrowser(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Action    string `json:"action"`
		DeviceID  string `json:"device_id"`
		TabID     string `json:"tab_id"`
		SessionID string `json:"session_id"`
		StartedAt int64  `json:"started_at"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case "targets", "start", "read", "stop", "cancel":
	default:
		replyError(w, "invalid recording action", 400)
		return
	}
	if req.DeviceID == "" {
		replyError(w, "select a connected browser", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := browser.DefaultHub.Call(ctx, uid, req.DeviceID, "recording_"+req.Action, map[string]any{"tab_id": req.TabID, "session_id": req.SessionID, "started_at": req.StartedAt})
	if err != nil {
		replyError(w, "Browser recording unavailable. Connect the browser extension and grant page access.", http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, result)
}

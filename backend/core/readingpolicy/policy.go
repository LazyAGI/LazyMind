// Package readingpolicy owns personal permission for new paper-reading work.
// Callers must resolve source/ACL and reusable artifacts before consulting it.
package readingpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"time"
)

const (
	OnDemand    = "on_demand"
	Automatic   = "automatic"
	Disabled    = "disabled"
	PDFParse    = "pdf_parse"
	AIStudy     = "ai_study"
	ChatPrepare = "chat_prepare"
	Formula     = "formula"
	Figure      = "figure"
	Table       = "table"
	PaperGuide  = "paper_guide"
)

var Keys = configurableKeys()

type Limits struct {
	DailyPDFDocuments     int `json:"daily_pdf_documents"`
	DailyStudyDocuments   int `json:"daily_study_documents"`
	DailyDocuments        int `json:"daily_documents,omitempty"`
	StudyItemsPerDocument int `json:"study_items_per_document,omitempty"`
	AutomaticConcurrency  int `json:"automatic_concurrency"`
}
type Policy struct {
	ParserRef              string            `json:"parser_ref"`
	StudyModelRef          string            `json:"study_model_ref"`
	Preset                 string            `json:"preset"`
	Modes                  map[string]string `json:"modes"`
	Limits                 Limits            `json:"limits"`
	AutomaticEligibleAfter *time.Time        `json:"automatic_eligible_after,omitempty"`
}

func Defaults() Policy {
	p := Policy{Preset: "demand", Modes: map[string]string{}, Limits: Limits{DailyPDFDocuments: 100, DailyStudyDocuments: 50, AutomaticConcurrency: 1}}
	for _, k := range Keys {
		p.Modes[k] = OnDemand
	}
	return p
}
func Normalize(p Policy) (Policy, error) {
	p = Migrate(p)
	switch p.Preset {
	case "demand", "advance", "full":
		p.Modes = Defaults().Modes
		if p.Preset != "demand" {
			p.Modes[PDFParse] = Automatic
		}
		if p.Preset == "full" {
			p.Modes[AIStudy] = Automatic
		}
	case "custom":
	default:
		return p, errors.New("请选择有效的准备预设")
	}
	if p.Modes != nil {
		if _, exists := p.Modes[PaperGuide]; !exists {
			p.Modes[PaperGuide] = OnDemand
		}
	}
	if p.Modes[PaperGuide] == Automatic {
		return p, errors.New("整篇导读仅支持按需或禁用")
	}
	if len(p.Modes) != len(Keys) {
		return p, errors.New("请提供所有阅读功能的准备方式")
	}
	for _, k := range Keys {
		m := p.Modes[k]
		if m != OnDemand && m != Automatic && m != Disabled {
			return p, fmt.Errorf("%s 准备方式无效", k)
		}
	}

	if p.Limits.DailyPDFDocuments < 1 || p.Limits.DailyPDFDocuments > 10000 || p.Limits.DailyStudyDocuments < 1 || p.Limits.DailyStudyDocuments > 10000 || p.Limits.AutomaticConcurrency != 1 {
		return p, errors.New("每日自动解析和精读额度须为 1–10000 篇；自动并发目前固定为 1")
	}
	return p, nil
}
func Load(ctx context.Context, db *gorm.DB, user string) (Policy, error) {
	p := Defaults()
	var row orm.UserReadingPolicy
	e := db.WithContext(ctx).Where("user_id = ?", user).Take(&row).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return p, nil
	}
	if e != nil {
		return p, e
	}
	return Decode([]byte(row.PolicyJSON))
}
func HasAutomatic(p Policy) bool {
	for _, m := range p.Modes {
		if m == Automatic {
			return true
		}
	}
	return false
}
func Eligible(p Policy, created time.Time) bool {
	return HasAutomatic(p) && p.AutomaticEligibleAfter != nil && created.After(*p.AutomaticEligibleAfter)
}
func Save(ctx context.Context, db *gorm.DB, user string, p Policy) (Policy, error) {
	_, includesGuide := p.Modes[PaperGuide]
	old, e := Load(ctx, db, user)
	if e != nil {
		return p, e
	}
	p, e = Normalize(p)
	if e != nil {
		return p, e
	}
	if !includesGuide {
		p.Modes[PaperGuide] = old.Modes[PaperGuide]
	}
	p.AutomaticEligibleAfter = old.AutomaticEligibleAfter
	// Only the preparation categories define automatic enrollment. The
	// independent on-demand guide must not exclude imports awaiting enrollment.
	preparationModesChanged := p.Modes[PDFParse] != old.Modes[PDFParse] || p.Modes[AIStudy] != old.Modes[AIStudy]
	if HasAutomatic(p) && (!HasAutomatic(old) || preparationModesChanged) {
		now := time.Now().UTC()
		p.AutomaticEligibleAfter = &now
	}
	b, e := json.Marshal(p)
	if e != nil {
		return p, e
	}
	e = db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"policy_json", "updated_at"})}).Create(&orm.UserReadingPolicy{UserID: user, PolicyJSON: string(b), UpdatedAt: time.Now().UTC()}).Error
	return p, e
}

type automaticKey struct{}

func AutomaticContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, automaticKey{}, true)
}
func Check(ctx context.Context, db *gorm.DB, user, key string) error {
	p, e := Load(ctx, db, user)
	if e != nil {
		return e
	}
	key = Category(key)
	if key == ChatPrepare {
		return nil
	}
	name := map[string]string{PDFParse: "PDF 解析", AIStudy: "AI 论文精读"}[key]
	if name == "" {
		name = key
	}
	if p.Modes[key] == Disabled {
		return fmt.Errorf("已禁止新的%s准备；已有结果仍可查看，请在 AI 阅读准备设置中修改", name)
	}
	if ctx.Value(automaticKey{}) == true && p.Modes[key] != Automatic {
		return fmt.Errorf("%s未开启自动准备；缺少结果时请开启自动或手动准备", name)
	}
	return nil
}

func IsAutomatic(ctx context.Context) bool { return ctx.Value(automaticKey{}) == true }

// Category maps internal block operations onto the two user-facing policies.
func Category(key string) string {
	switch key {
	case Formula, Figure, Table:
		return AIStudy
	}
	return key
}
func Mode(p Policy, key string) string { return p.Modes[Category(key)] }

// Migrate is a read-through migration: old JSON and results remain intact until
// the user saves. Mixed legacy study permissions never broaden authorization.
func Migrate(p Policy) Policy {
	if p.Limits.DailyDocuments != 0 {
		p.Limits.DailyPDFDocuments = p.Limits.DailyDocuments
		p.Limits.DailyStudyDocuments = p.Limits.DailyDocuments
	}
	if p.Limits.DailyPDFDocuments == 0 {
		p.Limits.DailyPDFDocuments = 100
	}
	if p.Limits.DailyStudyDocuments == 0 {
		p.Limits.DailyStudyDocuments = 50
	}
	if p.Limits.AutomaticConcurrency == 0 {
		p.Limits.AutomaticConcurrency = 1
	}
	p.Limits.DailyDocuments, p.Limits.StudyItemsPerDocument = 0, 0
	modes := map[string]string{PDFParse: p.Modes[PDFParse], AIStudy: p.Modes[AIStudy], PaperGuide: p.Modes[PaperGuide]}
	if modes[PaperGuide] == "" {
		modes[PaperGuide] = OnDemand
	}
	if modes[PDFParse] == "" {
		modes[PDFParse] = OnDemand
	}
	if modes[AIStudy] == "" {
		modes[AIStudy] = Automatic
		for _, key := range []string{Formula, Figure, Table} {
			m := p.Modes[key]
			if m == Disabled {
				modes[AIStudy] = Disabled
				break
			}
			if m != Automatic {
				modes[AIStudy] = OnDemand
			}
		}
	}
	p.Modes = modes
	return p
}
func Decode(raw []byte) (Policy, error) {
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.Preset == "" {
		p.Preset = "demand"
	}
	return Migrate(p), nil
}

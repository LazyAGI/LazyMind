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
	"reflect"
	"time"
)

const (
	OnDemand    = "on_demand"
	Automatic   = "automatic"
	Disabled    = "disabled"
	PDFParse    = "pdf_parse"
	ChatPrepare = "chat_prepare"
	Formula     = "formula"
	Figure      = "figure"
	Table       = "table"
)

var Keys = configurableKeys()

type Limits struct {
	DailyDocuments        int `json:"daily_documents"`
	StudyItemsPerDocument int `json:"study_items_per_document"`
	AutomaticConcurrency  int `json:"automatic_concurrency"`
}
type Policy struct {
	Preset                 string            `json:"preset"`
	Modes                  map[string]string `json:"modes"`
	Limits                 Limits            `json:"limits"`
	AutomaticEligibleAfter *time.Time        `json:"automatic_eligible_after,omitempty"`
}

func Defaults() Policy {
	p := Policy{Preset: "demand", Modes: map[string]string{}, Limits: Limits{5, 10, 1}}
	for _, k := range Keys {
		p.Modes[k] = OnDemand
	}
	return p
}
func Normalize(p Policy) (Policy, error) {
	switch p.Preset {
	case "demand", "advance", "full":
		p.Modes = Defaults().Modes
		if p.Preset != "demand" {
			p.Modes[PDFParse] = Automatic
		}
		if p.Preset == "full" {
			for _, k := range []string{Formula, Figure, Table} {
				p.Modes[k] = Automatic
			}
		}
	case "custom":
	default:
		return p, errors.New("请选择有效的准备预设")
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
	if p.Modes[PDFParse] != Automatic {
		for _, k := range []string{Formula, Figure, Table} {
			if p.Modes[k] == Automatic {
				return p, errors.New("自动研读需要自动准备结构化 PDF 解析，请先将解析设为自动或关闭自动研读")
			}
		}
	}
	if p.Limits.DailyDocuments < 1 || p.Limits.DailyDocuments > 100 || p.Limits.StudyItemsPerDocument < 1 || p.Limits.StudyItemsPerDocument > 100 || p.Limits.AutomaticConcurrency != 1 {
		return p, errors.New("每日自动文档和每篇研读数量须为 1–100；自动并发目前固定为 1")
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
	e = json.Unmarshal([]byte(row.PolicyJSON), &p)
	return p, e
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
	p, e := Normalize(p)
	if e != nil {
		return p, e
	}
	old, e := Load(ctx, db, user)
	if e != nil {
		return p, e
	}
	p.AutomaticEligibleAfter = old.AutomaticEligibleAfter
	if HasAutomatic(p) && (!HasAutomatic(old) || !reflect.DeepEqual(p.Modes, old.Modes)) {
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
	if p.Modes[key] == Disabled {
		return fmt.Errorf("已禁止新的%s准备；已有结果仍可查看，请在 AI 阅读准备设置中修改", key)
	}
	if ctx.Value(automaticKey{}) == true && p.Modes[key] != Automatic {
		return fmt.Errorf("%s自动准备已停止", key)
	}
	return nil
}

func IsAutomatic(ctx context.Context) bool { return ctx.Value(automaticKey{}) == true }

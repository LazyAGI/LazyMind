package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
	"sort"
	"strings"
	"time"
)

type paperReadReceipt struct {
	ContentHash   string    `json:"content_hash"`
	SourceVersion string    `json:"source_version"`
	Positions     []int     `json:"positions"`
	Pages         []int     `json:"pages"`
	Total         int       `json:"total_blocks"`
	Level         string    `json:"read_level"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func selectedContentPositions(pages []ContentPage, keywords []string, start, end int) ([]int, error) {
	if start < 0 || end < 0 || end < start || (start == 0 && end > 0) || len(keywords) > 12 {
		return nil, errors.New("invalid page range or keywords")
	}
	selected := map[int]bool{}
	if start > 0 {
		for i, p := range pages {
			if p.Page >= start && p.Page <= end {
				selected[i] = true
			}
		}
	} else if len(keywords) > 0 {
		for _, word := range keywords {
			if strings.TrimSpace(word) == "" || len(word) > 200 {
				return nil, errors.New("invalid keyword")
			}
		}
		for i, p := range pages {
			for _, word := range keywords {
				if strings.Contains(strings.ToLower(p.Text), strings.ToLower(word)) {
					for k := i - 1; k <= i+1; k++ {
						if k >= 0 && k < len(pages) {
							selected[k] = true
						}
					}
					break
				}
			}
		}
	} else {
		for i := range pages {
			selected[i] = true
		}
	}
	result := make([]int, 0, len(selected))
	for index := range selected {
		result = append(result, index)
	}
	sort.Ints(result)
	return result, nil
}
func recordPaperRead(ctx context.Context, user, expansion string, content orm.AcademicPaperContent, positions []int, pages []ContentPage) error {
	if expansion == "" || len(positions) == 0 {
		return nil
	}
	return conversationgroup.UserTransaction(ctx, store.DB(), user, func(tx *gorm.DB) error {
		var record orm.AcademicExpansion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", expansion, user).Take(&record).Error; err != nil {
			return err
		}
		ledger := map[string]paperReadReceipt{}
		if len(record.ReadingJSON) > 0 {
			if err := json.Unmarshal(record.ReadingJSON, &ledger); err != nil {
				return err
			}
		}
		entry := ledger[content.PaperID]
		if entry.ContentHash != content.ContentHash {
			entry = paperReadReceipt{ContentHash: content.ContentHash, SourceVersion: content.SourceVersion, Total: len(pages)}
		}
		seen := map[int]bool{}
		for _, p := range entry.Positions {
			seen[p] = true
		}
		for _, p := range positions {
			seen[p] = true
		}
		entry.Positions = []int{}
		entry.Pages = []int{}
		for p := range seen {
			entry.Positions = append(entry.Positions, p)
		}
		sort.Ints(entry.Positions)
		for _, p := range entry.Positions {
			entry.Pages = append(entry.Pages, pages[p].Page)
		}
		entry.Level = "related_sections"
		if len(entry.Positions) == len(pages) {
			entry.Level = "full"
		}
		entry.UpdatedAt = time.Now().UTC()
		ledger[content.PaperID] = entry
		return tx.Model(&record).Update("reading_json", marshal(ledger)).Error
	})
}

package academic

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
	"lazymind/core/store"
)

// The manual picker only shows papers with checked PDF sources.
func inspectSourcePDFs(ctx context.Context, db *gorm.DB, user string, source SourceContext, ids []string) ([]SourceInspection, error) {
	items, err := inspectSources(ctx, db, user, source, ids)
	if err != nil {
		return nil, err
	}
	for i := range items {
		item := &items[i]
		if !item.Eligible || item.Candidate.URL == "" || item.PDFVerified {
			continue
		}
		item.NeedsPDFLookup = true
		if shared := store.State(); shared != nil {
			checked, err := shared.Exists(ctx, sourcePDFCheckKey(user, source, *item))
			if err != nil {
				return nil, err
			}
			item.NeedsPDFLookup = !checked
		}
	}
	return items, nil
}

func sourcePDFCheckKey(user string, source SourceContext, row SourceInspection) string {
	return "academic:source-pdf:" + sourceHash([]any{user, source, row.SourceID, row.Requested, row.Candidate})
}

func checkSourcePDF(ctx context.Context, client *http.Client, user string, source SourceContext, row *SourceInspection) error {
	check, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	var err error
	if row.Candidate.SourceProvider == "arxiv" {
		err = checkOfficialArxivPDF(check, client, row.Candidate)
	} else {
		_, err = probePublicPDFCandidate(check, client, row.Candidate, row.Work.DOI)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		var upstream paperPDFHTTPError
		row.PDFLookupFailed = !errors.Is(err, errFulltextUnavailable) && !(errors.As(err, &upstream) && upstream.status == http.StatusNotFound)
		return nil
	}
	if shared := store.State(); shared != nil {
		if err := shared.Set(ctx, sourcePDFCheckKey(user, source, *row), []byte("checked"), importPreviewTTL); err != nil {
			return err
		}
	}
	row.NeedsPDFLookup = false
	return nil
}

func checkOfficialArxivPDF(ctx context.Context, client *http.Client, candidate FulltextCandidate) error {
	address, err := probePublicPDFCandidate(ctx, client, candidate, "")
	if err != nil {
		return err
	}
	return validateArxivPDFRedirect(candidate, address)
}

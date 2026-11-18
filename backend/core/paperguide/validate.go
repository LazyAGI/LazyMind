package paperguide

import (
	"fmt"
	"reflect"

	"lazymind/core/pdfir"
)

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidGuide, field) }

// ValidateSnapshot confirms the trusted identity and digest and rebuilds the
// derived catalog, so altered locators and cross-scope IDs cannot survive load.
func ValidateSnapshot(s GuideSourceSnapshot, identity SourceIdentity, irDigest string) error {
	if s.Identity != identity || !validSHA(irDigest) || s.IRDigest != irDigest {
		return fmt.Errorf("%w: identity or digest mismatch", ErrInvalidSource)
	}
	warnings := s.Coverage.Warnings
	if s.CatalogVersion == SourceCatalogVersion || s.CatalogVersion == sourceCatalogV5 || s.CatalogVersion == "paper-guide-source-v2" || s.CatalogVersion == "paper-guide-source-v3" || s.CatalogVersion == "paper-guide-source-v4" {
		warnings = s.SourceWarnings
	}
	if len(warnings) == 0 {
		warnings = nil
	}
	d := pdfir.Document{Precision: s.Precision, Version: s.IRVersion, ProjectionVersion: s.ProjectionVersion, SourceSHA256: s.Identity.SourceSHA256,
		Parser: s.Parser, CoordinateSpace: s.CoordinateSpace, Pages: s.Pages, Blocks: s.Blocks, Warnings: warnings}
	original, err := buildSourceSnapshot(d, identity, s.CatalogVersion, s.FigureProofs...)
	if err != nil || !reflect.DeepEqual(s, original) {
		return fmt.Errorf("%w: catalog mismatch", ErrInvalidSource)
	}
	return nil
}

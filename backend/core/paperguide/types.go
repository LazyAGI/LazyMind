// Package paperguide defines the fixed source and model-output contracts for a paper guide.
package paperguide

import "lazymind/core/pdfir"

const TaskVersion = "paper-guide-v15"
const SourceCatalogVersion = "paper-guide-source-v5"
const legacySourceNamespace = "paper-guide-v1"

type Role string

const (
	RoleBackground    Role = "background"
	RoleQuestion      Role = "question"
	RoleContribution  Role = "contribution"
	RoleJustification Role = "justification"
)

func Roles() [4]Role {
	return [4]Role{RoleBackground, RoleQuestion, RoleContribution, RoleJustification}
}

type ArgumentType string
type Attribution string
type SupportStatus string

const (
	ArgumentMethod        ArgumentType = "method_system"
	ArgumentExperiment    ArgumentType = "experiment_finding"
	ArgumentObservational ArgumentType = "observational_quantitative"
	ArgumentQualitative   ArgumentType = "qualitative_case"
	ArgumentTheory        ArgumentType = "theory_mathematics"
	ArgumentHumanities    ArgumentType = "historical_humanities_normative"
	ArgumentSynthesis     ArgumentType = "review_synthesis"
	ArgumentResource      ArgumentType = "dataset_benchmark_resource"

	AttributionPaper          Attribution = "paper_statement"
	AttributionTeaching       Attribution = "teaching"
	AttributionInterpretation Attribution = "interpretation"
	AttributionExternal       Attribution = "external_background"

	SupportSupported     SupportStatus = "supported"
	SupportPartial       SupportStatus = "partial"
	SupportUnsupported   SupportStatus = "unsupported"
	SupportUnavailable   SupportStatus = "unavailable"
	SupportNotApplicable SupportStatus = "not_applicable"
)

type SourceIdentity struct {
	SourceSHA256 string `json:"source_sha256"`
	DatasetID    string `json:"dataset_id"`
	DocumentID   string `json:"document_id"`
	OwnerID      string `json:"owner_id"`
}

type GuideSourceSnapshot struct {
	Precision         *pdfir.SourcePrecision `json:"source_precision,omitempty"`
	CatalogVersion    string                 `json:"catalog_version,omitempty"`
	SourceWarnings    []string               `json:"source_warnings,omitempty"`
	Identity          SourceIdentity         `json:"identity"`
	IRDigest          string                 `json:"ir_digest"`
	Parser            pdfir.Parser           `json:"parser"`
	IRVersion         int                    `json:"ir_version"`
	ProjectionVersion int                    `json:"projection_version"`
	CoordinateSpace   string                 `json:"coordinate_space"`
	Pages             []pdfir.Page           `json:"pages"`
	Coverage          GuideCoverage          `json:"coverage"`
	Blocks            []pdfir.Block          `json:"blocks"`
	Assets            []GuideAsset           `json:"assets"`
	Sections          []SourceSection        `json:"sections"`
	Evidence          []GuideEvidence        `json:"evidence"`
}

type GuideCoverage struct {
	Body         bool     `json:"body"`
	Conclusion   bool     `json:"conclusion"`
	Limitations  bool     `json:"limitations"`
	References   bool     `json:"references"`
	Appendix     bool     `json:"appendix"`
	AbstractOnly bool     `json:"abstract_only"`
	PageWarnings []int    `json:"page_warnings"`
	Warnings     []string `json:"warnings"`
}

type SourceSection struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Pages       []int    `json:"pages"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type GuideAsset struct {
	FragmentEvidenceIDs []string              `json:"fragment_evidence_ids,omitempty"`
	ID                  string                `json:"id"` // Same ID as its primary evidence entry.
	BlockID             string                `json:"block_id"`
	Kind                string                `json:"kind"`
	Locator             GuideLocator          `json:"locator"`
	RelatedBlocks       []pdfir.TableRelation `json:"related_blocks"`
	Warnings            []string              `json:"warnings"`
}

// GuideLocator is a tagged union. Pages are zero based; text ranges are half-open
// Unicode code point offsets into the original block. Rows/cells are physical
// indices into the parsed rows; a merged cell is identified by its origin.
type GuideLocator struct {
	Kind    string      `json:"kind"`
	Page    int         `json:"page"`
	BBox    []float64   `json:"bbox,omitempty"`
	Start   int         `json:"start,omitempty"`
	End     int         `json:"end,omitempty"`
	Cells   []GuideCell `json:"cells,omitempty"`
	Headers []string    `json:"headers,omitempty"`
	Latex   string      `json:"latex,omitempty"`
}
type GuideCell struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}
type GuideEvidence struct {
	ParentAssetID       string       `json:"parent_asset_id,omitempty"`
	FragmentEvidenceIDs []string     `json:"fragment_evidence_ids,omitempty"`
	SourceRegionID      string       `json:"source_region_id,omitempty"`
	ID                  string       `json:"id"`
	SourceBlockID       string       `json:"source_block_id"`
	SourceType          string       `json:"source_type"`
	Text                string       `json:"text"`
	Locator             GuideLocator `json:"locator"`
}

type GuideClaim struct {
	ID            string        `json:"id"`
	Statement     string        `json:"statement"`
	Attribution   Attribution   `json:"attribution"`
	EvidenceIDs   []string      `json:"evidence_ids"`
	ArgumentType  *ArgumentType `json:"argument_type"`
	Conditions    []string      `json:"conditions"`
	SupportStatus SupportStatus `json:"support_status"`
}

type GuideExtract struct {
	Claims        []GuideClaim `json:"claims"`
	OpenQuestions []string     `json:"open_questions"`
}
type GuideProfiles struct {
	Primary           *ArgumentType  `json:"primary"`
	Secondary         []ArgumentType `json:"secondary"`
	UncertaintyReason string         `json:"uncertainty_reason"`
}
type GuideSectionPlan struct {
	Role        Role     `json:"role"`
	Title       string   `json:"title"`
	Focus       string   `json:"focus"`
	Terms       []string `json:"terms"`
	ClaimIDs    []string `json:"claim_ids"`
	EvidenceIDs []string `json:"evidence_ids"`
	AssetIDs    []string `json:"asset_ids"`
}
type GuideAssetDecision struct {
	ID       string `json:"id"`
	Included bool   `json:"included"`
	Reason   string `json:"reason"`
}
type GuidePlan struct {
	Profiles       GuideProfiles        `json:"profiles"`
	Questions      []string             `json:"questions"`
	Claims         []GuideClaim         `json:"claims"`
	Sections       []GuideSectionPlan   `json:"sections"`
	TermPolicy     []string             `json:"term_policy"`
	RequiredAssets []GuideAssetDecision `json:"required_assets"`
}
type GuideBlock struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Text        string      `json:"text"`
	Attribution Attribution `json:"attribution"`
	ClaimIDs    []string    `json:"claim_ids"`
	EvidenceIDs []string    `json:"evidence_ids"`
	AssetID     string      `json:"asset_id,omitempty"`
}
type GuideSection struct {
	Role              Role         `json:"role"`
	Title             string       `json:"title"`
	Blocks            []GuideBlock `json:"blocks"`
	Claims            []GuideClaim `json:"claims"`
	ClaimIDs          []string     `json:"claim_ids"`
	Summary           string       `json:"summary"`
	Version           int          `json:"version,omitempty"`            // Server-owned.
	PublicationStatus string       `json:"publication_status,omitempty"` // Server-owned.
}
type GuideFinding struct {
	TargetID   string `json:"target_id"`
	TargetType string `json:"target_type"`
	IssueType  string `json:"issue_type"`
	Suggestion string `json:"suggestion"`
}
type GuideReview struct {
	Findings []GuideFinding `json:"findings"`
}

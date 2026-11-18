package desktopupdate

// Config is supplied by the packaged helper, never by a downloaded policy.
type Config struct {
	TrustDomain     string            `json:"trustDomain"`
	Audience        string            `json:"audience"`
	Channel         string            `json:"channel"`
	Mode            string            `json:"mode"`
	ReleaseID       string            `json:"releaseId"`
	Version         string            `json:"version"`
	Platform        string            `json:"platform"`
	Arch            string            `json:"arch"`
	SigningIdentity string            `json:"signingIdentity"`
	UpdaterProtocol int               `json:"updaterProtocol"`
	PolicyURL       string            `json:"policyURL"`
	AllowedOrigins  []string          `json:"allowedOrigins"`
	PublicKeys      map[string]string `json:"publicKeys"`
}

// LocalState is measured locally; unknown/dirty data must never be inferred
// compatible merely from the current application version.
type LocalState struct {
	SystemVersion     string                         `json:"systemVersion"`
	DataProfile       string                         `json:"dataProfile"`
	Dirty             bool                           `json:"dirty"`
	Components        map[string]string              `json:"components"`
	ComponentMetadata map[string]ComponentDescriptor `json:"componentMetadata,omitempty"`
}

type Decision struct {
	Status         string   `json:"status"`
	Reason         string   `json:"reason"`
	Route          []string `json:"route,omitempty"`
	PolicySequence uint64   `json:"policySequence,omitempty"`
	PolicyDigest   string   `json:"policyDigest,omitempty"`
	Next           *Release `json:"next,omitempty"`
	Source         *Release `json:"source,omitempty"`
	Edge           *Edge    `json:"edge,omitempty"`
	TargetVersion  string   `json:"targetVersion,omitempty"`
}

type Artifact struct {
	UnpackedBytes int64  `json:"unpackedBytes,omitempty"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
}

type Release struct {
	ID                   string   `json:"releaseId"`
	Version              string   `json:"version"`
	Platform             string   `json:"platform"`
	Arch                 string   `json:"arch"`
	MinimumSystemVersion string   `json:"minimumSystemVersion"`
	MinUpdaterProtocol   int      `json:"minUpdaterProtocol"`
	UpdaterProtocol      int      `json:"updaterProtocol"`
	SigningIdentity      string   `json:"signingIdentity"`
	Package              Artifact `json:"package"`
	Feed                 Artifact `json:"feed"`
	Components           Artifact `json:"components"`
}

type RecoveryContract struct {
	Version              int    `json:"version"`
	SourcePackageSHA256  string `json:"sourcePackageSHA256"`
	SourceManifestSHA256 string `json:"sourceManifestSHA256"`
	HelperProtocolMin    int    `json:"helperProtocolMin"`
	HelperProtocolMax    int    `json:"helperProtocolMax"`
	Direction            string `json:"direction"`
}

type ComponentDescriptor struct {
	URL             string            `json:"url,omitempty"`
	FallbackURL     string            `json:"fallbackUrl,omitempty"`
	CatalogURL      string            `json:"catalogUrl,omitempty"`
	Filename        string            `json:"filename,omitempty"`
	SizeBytes       int64             `json:"sizeBytes,omitempty"`
	UnpackedBytes   int64             `json:"unpackedBytes,omitempty"`
	ID              string            `json:"id"`
	Revision        string            `json:"revision"`
	SHA256          string            `json:"sha256"`
	Tag             string            `json:"tag"`
	PythonABI       string            `json:"pythonAbi"`
	BaseFingerprint string            `json:"baseFingerprint"`
	Platform        string            `json:"platform"`
	Arch            string            `json:"arch"`
	Packages        map[string]string `json:"packages"`
}
type ComponentSourceClass struct {
	Tag             string            `json:"tag"`
	PythonABI       string            `json:"pythonAbi"`
	BaseFingerprint string            `json:"baseFingerprint"`
	Packages        map[string]string `json:"packages"`
}
type ComponentTransition struct {
	SourceClasses          []ComponentSourceClass `json:"sourceClasses"`
	TargetComponentsSHA256 string                 `json:"targetComponentsSHA256"`
}
type RepairAcceptance struct {
	SourceID               string                         `json:"sourceReleaseId"`
	FailedTargetID         string                         `json:"failedTargetReleaseId"`
	RepairID               string                         `json:"repairReleaseId"`
	SourceDataProfile      string                         `json:"sourceDataProfile"`
	TargetDataProfile      string                         `json:"targetDataProfile"`
	ComponentTransitions   map[string]ComponentTransition `json:"componentTransitions"`
	TargetComponentsSHA256 string                         `json:"targetComponentsSHA256"`
}
type Edge struct {
	ComponentTransitions map[string]ComponentTransition `json:"componentTransitions,omitempty"`
	SourceID             string                         `json:"sourceReleaseId"`
	TargetID             string                         `json:"targetReleaseId"`
	Priority             int                            `json:"priority"`
	MigrationProfile     string                         `json:"migrationProfile"`
	SourceDataProfiles   []string                       `json:"sourceDataProfiles"`
	TargetDataProfile    string                         `json:"targetDataProfile"`
	RequiredComponents   map[string]string              `json:"requiredComponents"`
	Recovery             RecoveryContract               `json:"recoveryContract"`
}

type Policy struct {
	RepairAcceptances  []RepairAcceptance `json:"repairAcceptances,omitempty"`
	SchemaVersion      int                `json:"schemaVersion"`
	MinUpdaterProtocol int                `json:"minUpdaterProtocol"`
	TrustDomain        string             `json:"trustDomain"`
	Audience           string             `json:"audience"`
	Channel            string             `json:"channel"`
	Sequence           uint64             `json:"policySequence"`
	IssuedAt           string             `json:"issuedAt"`
	ExpiresAt          string             `json:"expiresAt"`
	Releases           []Release          `json:"releases"`
	Edges              []Edge             `json:"edges"`
	Revoked            []string           `json:"revoked"`
}

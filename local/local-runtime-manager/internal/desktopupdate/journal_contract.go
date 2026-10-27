package desktopupdate

type ProcessIdentity struct {
	PID     int    `json:"pid"`
	StartID uint64 `json:"startId,string"`
}

type UpdateScope struct {
	Installation string   `json:"installation"`
	DataRoots    []string `json:"dataRoots"`
}

type SnapshotUnit struct {
	Path    string   `json:"path"`
	Exclude []string `json:"exclude,omitempty"`
}

type InstallSpaceReservation struct {
	Directory     string `json:"directory"`
	InstallBytes  int64  `json:"installBytes"`
	RecoveryBytes int64  `json:"recoveryBytes"`
}

type SnapshotRecord struct {
	ActivePath string `json:"activePath"`
	BackupPath string `json:"backupPath"`
	ReadyPath  string `json:"readyPath"`
	FailedPath string `json:"failedPath"`
	Existed    bool   `json:"existed"`
	Digest     string `json:"digest"`
}

type SnapshotManifest struct {
	Version       int                   `json:"version"`
	TransactionID string                `json:"transactionId"`
	Units         []SnapshotRecord      `json:"units"`
	Reservations  []SnapshotReservation `json:"reservations"`
	Complete      bool                  `json:"complete"`
}

type SnapshotReservation struct {
	InstallPath   string `json:"installPath"`
	RecoveryPath  string `json:"recoveryPath"`
	InstallBytes  int64  `json:"installBytes"`
	RecoveryBytes int64  `json:"recoveryBytes"`
}

type TransactionPlan struct {
	TrustDomain          string                    `json:"trustDomain"`
	Audience             string                    `json:"audience"`
	Channel              string                    `json:"channel"`
	Mode                 string                    `json:"mode"`
	Platform             string                    `json:"platform"`
	Arch                 string                    `json:"arch"`
	SigningIdentity      string                    `json:"signingIdentity"`
	UpdaterProtocol      int                       `json:"updaterProtocol"`
	Scope                UpdateScope               `json:"scope"`
	SourceID             string                    `json:"sourceId"`
	TargetID             string                    `json:"targetId"`
	SourceVersion        string                    `json:"sourceVersion"`
	TargetVersion        string                    `json:"targetVersion"`
	SourceManifestSHA256 string                    `json:"sourceManifestSHA256"`
	SourcePackageSHA256  string                    `json:"sourcePackageSHA256"`
	TargetPackageSHA256  string                    `json:"targetPackageSHA256"`
	PolicySequence       uint64                    `json:"policySequence"`
	PolicyDigest         string                    `json:"policyDigest"`
	DataProfile          string                    `json:"dataProfile"`
	TargetDataProfile    string                    `json:"targetDataProfile"`
	Units                []SnapshotUnit            `json:"units"`
	SQLiteFiles          []string                  `json:"sqliteFiles,omitempty"`
	Reservations         []InstallSpaceReservation `json:"reservations"`
	OwnerToken           string                    `json:"-"`
}

type UpdateJournal struct {
	Version            int               `json:"version"`
	ID                 string            `json:"id"`
	Stage              string            `json:"stage"`
	Revision           uint64            `json:"revision"`
	Plan               TransactionPlan   `json:"plan"`
	ResolvedScope      UpdateScope       `json:"resolvedScope"`
	SourceProcesses    []ProcessIdentity `json:"sourceProcesses"`
	SourceCoordinators []ProcessIdentity `json:"sourceCoordinators"`
	SnapshotPath       string            `json:"snapshotPath,omitempty"`
	SnapshotSHA256     string            `json:"snapshotSHA256,omitempty"`
}

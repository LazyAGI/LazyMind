package filediff

type Content struct {
	Path         string
	Data         []byte
	Mime         string
	FileType     string
	Binary       bool
	EditableText bool
	Size         int64
}

type Options struct {
	ContextLines int
	Mode         string
	OldStart     int
	NewStart     int
	Lines        int
}

type FileDiff struct {
	Path              string          `json:"path"`
	Status            string          `json:"status"`
	Binary            bool            `json:"binary"`
	EditableText      bool            `json:"editable_text"`
	Supported         bool            `json:"supported"`
	UnsupportedReason string          `json:"unsupported_reason,omitempty"`
	TooLarge          bool            `json:"too_large"`
	HunkCount         int             `json:"hunk_count"`
	DiffEntryLines    []DiffEntryLine `json:"diff_entry_lines"`
}

type DiffEntryLine struct {
	Type                    string `json:"type"`
	Text                    string `json:"text"`
	HTML                    string `json:"html"`
	OldLine                 int    `json:"old_line"`
	NewLine                 int    `json:"new_line"`
	DisplayNoNewLineWarning bool   `json:"display_no_new_line_warning,omitempty"`
	HunkID                  string `json:"hunk_id,omitempty"`
	Decision                string `json:"decision,omitempty"`
	OldStart                int    `json:"old_start,omitempty"`
	OldLines                int    `json:"old_lines,omitempty"`
	NewStart                int    `json:"new_start,omitempty"`
	NewLines                int    `json:"new_lines,omitempty"`
	wholeLineReplacement    bool
}

type Differ interface {
	CompareContent(old Content, next Content, opts Options) (FileDiff, error)
}

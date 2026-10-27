package translation

import (
	"strings"
	"time"
)

// providerSpec keeps free-tier limits close to provider dispatch so document
// translation and selection translation use the same safe request sizes.
type providerSpec struct {
	Name              string
	Keyless           bool
	MaxChunkRunes     int
	MaxInputBytes     int
	MinInterval       time.Duration
	SupportsDocuments bool
}

var translationProviders = map[string]providerSpec{
	"tencent translation":      {Name: "Tencent Translation", MaxChunkRunes: 1800, MinInterval: 275 * time.Millisecond, SupportsDocuments: true},
	"google translate web":     {Name: "Google Translate Web", Keyless: true, MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"tencent transmart web":    {Name: "Tencent TranSmart Web", Keyless: true, MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"mymemory":                 {Name: "MyMemory", Keyless: true, MaxInputBytes: 500, MinInterval: time.Second, SupportsDocuments: false},
	"baidu translation":        {Name: "Baidu Translation", MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"niutrans":                 {Name: "Niutrans", MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"aliyun translation":       {Name: "Aliyun Translation", MaxChunkRunes: 1000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"volcengine translation":   {Name: "Volcengine Translation", MaxChunkRunes: 1000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"microsoft translator":     {Name: "Microsoft Translator", MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"google cloud translation": {Name: "Google Cloud Translation", MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"youdao translation":       {Name: "Youdao Translation", MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"deepl":                    {Name: "DeepL", MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"caiyun translation":       {Name: "Caiyun Translation", MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	// At most 750 Unicode scalars are 3000 UTF-8 bytes. This leaves room in
	// TokenHub's 4k input-token limit for the instruction and tokenizer overhead.
	"tencent tokenhub translation": {Name: "Tencent TokenHub Translation", MaxChunkRunes: 750, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
}

func lookupProvider(name string) (providerSpec, bool) {
	provider, ok := translationProviders[strings.ToLower(strings.TrimSpace(name))]
	return provider, ok
}

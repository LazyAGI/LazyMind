package modelprovider

import (
	"strings"
	"time"
)

// providerSpec keeps free-tier limits close to provider dispatch so document
// translation and selection translation use the same safe request sizes.
type TranslationProviderSpec struct {
	Name              string
	Available         bool
	Keyless           bool
	MaxChunkRunes     int
	MaxInputBytes     int
	MinInterval       time.Duration
	SupportsDocuments bool
}

var translationProviders = map[string]TranslationProviderSpec{
	"google translate web":     {Name: "Google Translate Web", Available: true, Keyless: true, MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"bing translate web":       {Name: "Bing Translate Web", Available: true, Keyless: true, MaxChunkRunes: 800, MinInterval: time.Second, SupportsDocuments: true},
	"sogou translate web":      {Name: "Sogou Translate Web", Available: true, Keyless: true, MaxChunkRunes: 500, MinInterval: time.Second, SupportsDocuments: true},
	"deepl web":                {Name: "DeepL Web", Available: true, Keyless: true, MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"tencent transmart web":    {Name: "Tencent TranSmart Web", Available: true, Keyless: true, MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"baidu translation":        {Name: "Baidu Translation", Available: true, MaxChunkRunes: 1000, MinInterval: time.Second, SupportsDocuments: true},
	"niutrans":                 {Name: "Niutrans", Available: true, MaxChunkRunes: 5000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
	"aliyun translation":       {Name: "Aliyun Translation", Available: true, MaxChunkRunes: 1000, MinInterval: 200 * time.Millisecond, SupportsDocuments: true},
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

// TranslationProvider returns the single runtime declaration, including paused adapters.
func TranslationProvider(name string) (TranslationProviderSpec, bool) {
	provider, ok := translationProviders[strings.ToLower(strings.TrimSpace(name))]
	return provider, ok
}

func IsTranslationProviderAvailable(name string) bool {
	provider, ok := TranslationProvider(name)
	return ok && provider.Available
}

func IsKeylessTranslationProvider(name string) bool {
	provider, ok := TranslationProvider(name)
	return ok && provider.Available && provider.Keyless
}

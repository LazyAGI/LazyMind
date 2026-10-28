package translation

import "testing"

func TestTranslationProviderCatalogIncludesFreeSources(t *testing.T) {
	for _, name := range []string{
		"Tencent Translation", "Google Translate Web", "Tencent TranSmart Web", "MyMemory",
		"Bing Translate Web", "Sogou Translate Web", "DeepL Web",
		"Baidu Translation", "Niutrans", "Aliyun Translation", "Volcengine Translation",
		"Microsoft Translator", "Google Cloud Translation", "Youdao Translation", "DeepL",
		"Caiyun Translation", "Tencent TokenHub Translation",
	} {
		if _, ok := lookupProvider(name); !ok {
			t.Errorf("missing translation provider %q", name)
		}
	}
}

func TestTranslationProviderLimits(t *testing.T) {
	baidu, ok := lookupProvider("Baidu Translation")
	if !ok {
		t.Fatal("Baidu provider missing")
	}
	if baidu.MaxChunkRunes > 1000 || baidu.MinInterval < 1000000000 {
		t.Fatalf("Baidu limits: %+v", baidu)
	}
	memory, ok := lookupProvider("MyMemory")
	if !ok {
		t.Fatal("MyMemory provider missing")
	}
	if memory.SupportsDocuments || memory.MaxInputBytes > 500 {
		t.Fatalf("MyMemory limits: %+v", memory)
	}
	for _, name := range []string{"Bing Translate Web", "Sogou Translate Web", "DeepL Web"} {
		provider, ok := lookupProvider(name)
		if !ok || !provider.Keyless || !provider.SupportsDocuments || provider.MaxChunkRunes <= 0 || provider.MinInterval <= 0 {
			t.Fatalf("web provider %q has invalid limits: %+v, found=%v", name, provider, ok)
		}
	}
}

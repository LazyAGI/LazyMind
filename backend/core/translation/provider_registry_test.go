package translation

import "testing"

func TestTranslationProviderCatalogIncludesFreeSources(t *testing.T) {
	for _, name := range []string{
		"Tencent Translation", "Google Translate Web", "Tencent TranSmart Web", "MyMemory",
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
}

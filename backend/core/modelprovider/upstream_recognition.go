package modelprovider

import (
	"strings"
	"sync/atomic"
)

type upstreamRecognitionCandidate struct {
	modelType string
	conflict  bool
}

// Every map is immutable after construction. Existing unknown candidates and
// conflicting types both block lookup in broader scopes.
type upstreamModelRecognitionIndex struct {
	providers map[string]map[string]upstreamRecognitionCandidate
	exact     map[string]upstreamRecognitionCandidate
	basenames map[string]upstreamRecognitionCandidate
}

var upstreamModelRecognition atomic.Pointer[upstreamModelRecognitionIndex]

func newUpstreamModelRecognitionIndex(catalog upstreamCatalogFile) *upstreamModelRecognitionIndex {
	index := &upstreamModelRecognitionIndex{
		providers: make(map[string]map[string]upstreamRecognitionCandidate),
		exact:     make(map[string]upstreamRecognitionCandidate),
		basenames: make(map[string]upstreamRecognitionCandidate),
	}
	add := func(models map[string]upstreamRecognitionCandidate, key, modelType string) {
		if key == "" {
			return
		}
		candidate := models[key]
		if modelType != "" {
			if candidate.modelType != "" && candidate.modelType != modelType {
				candidate.conflict = true
			} else {
				candidate.modelType = modelType
			}
		}
		models[key] = candidate
	}
	addModel := func(provider string, model upstreamCatalogModel) {
		modelType := normalizedDeclaredModelType(model.ModelType)
		switch modelType {
		case "llm", "vlm", "embed", "multimodal_embedding", "rerank", "stt", "tts", "text2image", "text2video", "image_editing":
		default:
			modelType = ""
		}
		key := strings.ToLower(strings.TrimSpace(model.Name))
		if provider != "" {
			if index.providers[provider] == nil {
				index.providers[provider] = make(map[string]upstreamRecognitionCandidate)
			}
			add(index.providers[provider], key, modelType)
		}
		add(index.exact, key, modelType)
		add(index.basenames, recognitionModelBasename(key), modelType)
	}
	for provider, models := range catalog.Providers {
		for _, model := range models {
			addModel(LazyLLMSource(provider), model)
		}
	}
	for _, model := range catalog.RecognitionModels {
		addModel("", model)
	}
	return index
}

func (index *upstreamModelRecognitionIndex) modelType(providerName, modelID string) string {
	if index == nil {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(modelID))
	if value, found := index.providers[LazyLLMSource(providerName)][key]; found {
		return value.resolvedType()
	}
	if value, found := index.exact[key]; found {
		return value.resolvedType()
	}
	return index.basenames[recognitionModelBasename(key)].resolvedType()
}

func (candidate upstreamRecognitionCandidate) resolvedType() string {
	if candidate.conflict {
		return ""
	}
	return candidate.modelType
}

// Strip directory/vendor prefixes only. Version, size and pricing suffixes are
// part of the model identity and are never rewritten for recognition.
func recognitionModelBasename(modelID string) string {
	if slash := strings.LastIndexByte(modelID, '/'); slash >= 0 {
		return modelID[slash+1:]
	}
	return modelID
}

func loadUpstreamModelRecognitionIndex(path string) (*upstreamModelRecognitionIndex, error) {
	catalog, err := loadUpstreamCatalog(path)
	if err != nil {
		return nil, err
	}
	return newUpstreamModelRecognitionIndex(catalog), nil
}

// LoadUpstreamModelRecognitionCatalog replaces the read-only discovery index.
func LoadUpstreamModelRecognitionCatalog(path string) error {
	index, err := loadUpstreamModelRecognitionIndex(path)
	if err != nil {
		return err
	}
	upstreamModelRecognition.Store(index)
	return nil
}

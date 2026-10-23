package modelprovider

import (
	"sort"
	"strings"
	"sync/atomic"
)

type upstreamRecognitionCandidate struct {
	modelType   string
	conflict    bool
	input       []string
	output      []string
	outputKnown bool
}

// Every map is immutable after construction. Existing unknown candidates and
// conflicting types both block lookup in broader scopes.
type upstreamModelRecognitionIndex struct {
	providers      map[string]map[string]upstreamRecognitionCandidate
	exact          map[string]upstreamRecognitionCandidate
	basenames      map[string]upstreamRecognitionCandidate
	fuzzyBasenames map[string]upstreamRecognitionCandidate
}

var upstreamModelRecognition atomic.Pointer[upstreamModelRecognitionIndex]

func newUpstreamModelRecognitionIndex(catalog upstreamCatalogFile) *upstreamModelRecognitionIndex {
	index := &upstreamModelRecognitionIndex{
		providers:      make(map[string]map[string]upstreamRecognitionCandidate),
		exact:          make(map[string]upstreamRecognitionCandidate),
		basenames:      make(map[string]upstreamRecognitionCandidate),
		fuzzyBasenames: make(map[string]upstreamRecognitionCandidate),
	}
	add := func(models map[string]upstreamRecognitionCandidate, key, modelType string, model upstreamCatalogModel) {
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
			// Input capabilities accumulate only for the confirmed typed identity.
			// Outputs must agree across sources so catalog enrichment cannot invent
			// a different generation task. Missing outputs do not assert absence.
			input := modalitySet(candidate.input)
			for _, value := range model.Input {
				input[strings.ToLower(strings.TrimSpace(value))] = true
			}
			candidate.input = recognitionModalities(input)
			output := modalitySet(model.Output)
			delete(output, "")
			if len(output) > 0 {
				if candidate.outputKnown {
					existingOutput := modalitySet(candidate.output)
					for value := range output {
						if !existingOutput[value] {
							delete(output, value)
						}
					}
				}
				candidate.output = recognitionModalities(output)
				candidate.outputKnown = true
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
			add(index.providers[provider], key, modelType, model)
		}
		add(index.exact, key, modelType, model)
		add(index.basenames, recognitionModelBasename(key), modelType, model)
		add(index.fuzzyBasenames, recognitionFuzzyBasename(key), modelType, model)
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
	candidate, found := index.lookupExact(providerName, modelID)
	if !found {
		candidate, _ = index.lookupFuzzy(modelID)
	}
	return candidate.resolvedType()
}

// A found but unresolved identity blocks fallback to weaker names or paths.
func (index *upstreamModelRecognitionIndex) lookupExact(providerName, modelID string) (upstreamRecognitionCandidate, bool) {
	if index == nil {
		return upstreamRecognitionCandidate{}, false
	}
	key := strings.ToLower(strings.TrimSpace(modelID))
	if value, found := index.providers[LazyLLMSource(providerName)][key]; found {
		return value, true
	}
	if value, found := index.exact[key]; found {
		return value, true
	}
	value, found := index.basenames[recognitionModelBasename(key)]
	return value, found
}

// Separator aliases are the last lookup scope; exact unknown/conflicting
// identities must be checked before calling this method.
func (index *upstreamModelRecognitionIndex) lookupFuzzy(modelID string) (upstreamRecognitionCandidate, bool) {
	if index == nil {
		return upstreamRecognitionCandidate{}, false
	}
	value, found := index.fuzzyBasenames[recognitionFuzzyBasename(modelID)]
	return value, found
}

func recognitionModalities(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (candidate upstreamRecognitionCandidate) resolvedType() string {
	if candidate.conflict {
		return ""
	}
	return candidate.modelType
}

// Exact lookup strips directory/vendor prefixes only. Fuzzy lookup may ignore
// separators, but always retains version/parameter digits and pricing suffixes.
func recognitionModelBasename(modelID string) string {
	if slash := strings.LastIndexByte(modelID, '/'); slash >= 0 {
		return modelID[slash+1:]
	}
	return modelID
}

func recognitionFuzzyBasename(modelID string) string {
	name := recognitionModelBasename(strings.ToLower(strings.TrimSpace(modelID)))
	return strings.NewReplacer(".", "", "-", "", "_", "").Replace(name)
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

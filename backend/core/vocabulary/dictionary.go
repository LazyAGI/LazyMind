package vocabulary

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
)

type DictionarySource interface {
	Lookup(context.Context, string, string) ([]DictionaryEntry, error)
	Name() string
	Version() string
	License() string
	Priority() int
}
type freeDictRecord struct {
	Term        string   `json:"term"`
	Phonetic    string   `json:"phonetic"`
	POS         string   `json:"pos"`
	Translation string   `json:"translation"`
	Definition  string   `json:"definition"`
	Examples    []string `json:"examples"`
}

//go:embed dictionary_data/freedict-eng-zho.json.gz
var freeDictCompressed []byte

//go:embed dictionary_data/ecdict-common.json.gz
var ecdictCompressed []byte

type freeDictSource struct {
	once  sync.Once
	index map[string][]freeDictRecord
	err   error
}

type ecdictSource struct {
	once  sync.Once
	index map[string][]freeDictRecord
	err   error
}

func (*ecdictSource) Name() string    { return "ECDICT" }
func (*ecdictSource) Version() string { return "bc015ed2e24a" }
func (*ecdictSource) License() string { return "MIT" }
func (*ecdictSource) Priority() int   { return 1 }
func (d *ecdictSource) load() {
	d.once.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(ecdictCompressed))
		if err != nil {
			d.err = err
			return
		}
		defer reader.Close()
		var rows []freeDictRecord
		if err = json.NewDecoder(reader).Decode(&rows); err != nil {
			d.err = err
			return
		}
		d.index = map[string][]freeDictRecord{}
		for _, row := range rows {
			d.index[normalizeTerm(row.Term)] = append(d.index[normalizeTerm(row.Term)], row)
		}
	})
}
func (d *ecdictSource) Lookup(_ context.Context, language, term string) ([]DictionaryEntry, error) {
	if language != "" && language != "en" {
		return nil, nil
	}
	d.load()
	if d.err != nil {
		return nil, d.err
	}
	rows := d.index[normalizeTerm(term)]
	out := make([]DictionaryEntry, 0, len(rows))
	for _, row := range rows {
		id := stableDictionaryID(d.Name(), d.Version(), row.Term)
		pos := strings.TrimSpace(strings.Split(strings.Split(row.POS, "/")[0], ":")[0])
		entry := DictionaryEntry{ID: id, Language: "en", NormalizedTerm: normalizeTerm(row.Term), Term: row.Term, Phonetic: row.Phonetic, SourceName: d.Name(), SourceVersion: d.Version(), LicenseID: d.License(), SourceLocator: "entry:" + row.Term, Priority: d.Priority()}
		entry.Senses = []DictionarySense{{ID: stableDictionaryID(id, "sense", "0"), EntryID: id, PartOfSpeech: pos, Definition: row.Definition, Translation: row.Translation}}
		out = append(out, entry)
	}
	return out, nil
}

func (*freeDictSource) Name() string    { return "FreeDict eng-zho" }
func (*freeDictSource) Version() string { return "2025.11.23" }
func (*freeDictSource) License() string { return "CC-BY-SA-3.0" }
func (*freeDictSource) Priority() int   { return 10 }
func (d *freeDictSource) load() {
	d.once.Do(func() {
		reader, err := gzip.NewReader(strings.NewReader(string(freeDictCompressed)))
		if err != nil {
			d.err = err
			return
		}
		defer reader.Close()
		raw, err := io.ReadAll(reader)
		if err != nil {
			d.err = err
			return
		}
		var rows []freeDictRecord
		if err = json.Unmarshal(raw, &rows); err != nil {
			d.err = err
			return
		}
		d.index = map[string][]freeDictRecord{}
		for _, row := range rows {
			key := normalizeTerm(row.Term)
			d.index[key] = append(d.index[key], row)
		}
	})
}
func stableDictionaryID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
func (d *freeDictSource) Lookup(_ context.Context, language, term string) ([]DictionaryEntry, error) {
	if language != "" && language != "en" {
		return nil, nil
	}
	d.load()
	if d.err != nil {
		return nil, d.err
	}
	records := d.index[normalizeTerm(term)]
	out := make([]DictionaryEntry, 0, len(records))
	for _, row := range records {
		// FreeDict occasionally exposes an obsolete unit or slang sense as the
		// only translation for a common modern word. Do not auto-fill those
		// entries; an empty, editable result is safer than confidently wrong data.
		definition := strings.ToLower(row.Definition)
		if len([]rune(strings.TrimSpace(row.Translation))) <= 1 && (strings.Contains(definition, "historical") || strings.Contains(definition, "obsolete")) {
			continue
		}
		id := stableDictionaryID(d.Name(), d.Version(), row.Term)
		entry := DictionaryEntry{ID: id, Language: "en", NormalizedTerm: normalizeTerm(row.Term), Term: row.Term, Phonetic: row.Phonetic, SourceName: d.Name(), SourceVersion: d.Version(), LicenseID: d.License(), SourceLocator: "entry:" + row.Term, Priority: d.Priority()}
		entry.Senses = []DictionarySense{{ID: stableDictionaryID(id, "sense", "0"), EntryID: id, PartOfSpeech: row.POS, Definition: row.Definition, Translation: row.Translation}}
		for i, sentence := range row.Examples {
			entry.Examples = append(entry.Examples, DictionaryExample{ID: stableDictionaryID(id, "example", string(rune(i))), EntryID: id, Sentence: sentence, SourceLocator: "entry:" + row.Term, ExampleOrder: i})
		}
		out = append(out, entry)
	}
	return out, nil
}

type DictionaryResolver struct{ sources []DictionarySource }

func NewDictionaryResolver(sources ...DictionarySource) *DictionaryResolver {
	return &DictionaryResolver{sources: sources}
}
func (r *DictionaryResolver) Lookup(ctx context.Context, language, term string) ([]DictionaryEntry, error) {
	if len(r.sources) == 0 {
		return nil, errors.New("no dictionary sources configured")
	}
	var out []DictionaryEntry
	seen := map[string]bool{}
	for _, source := range r.sources {
		entries, err := source.Lookup(ctx, language, term)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			key := normalizeTerm(entry.Term) + "\x00" + entry.SourceName
			if !seen[key] {
				seen[key] = true
				out = append(out, entry)
			}
		}
	}
	return out, nil
}

var bundledDictionaries = NewDictionaryResolver(&ecdictSource{}, &freeDictSource{})

func dictionaryReviewCandidates(term string, count int) []freeDictRecord {
	source, ok := bundledDictionaries.sources[0].(*ecdictSource)
	if !ok || count < 1 {
		return nil
	}
	source.load()
	target := normalizeTerm(term)
	type ranked struct {
		row   freeDictRecord
		score int
		hash  string
	}
	rows := make([]ranked, 0, len(source.index))
	for key, records := range source.index {
		if key == target || len(records) == 0 || strings.TrimSpace(records[0].Translation) == "" {
			continue
		}
		score := spellingSimilarity(target, key)
		sum := sha256.Sum256([]byte(target + "\x00" + key))
		rows = append(rows, ranked{records[0], score, hex.EncodeToString(sum[:])})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].hash < rows[j].hash
	})
	out, used := make([]freeDictRecord, 0, count), map[string]bool{}
	for _, row := range rows {
		if row.score < 2 || len(out) >= min(3, count) {
			continue
		}
		key := normalizeTerm(row.row.Term)
		if !used[key] {
			out = append(out, row.row)
			used[key] = true
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].hash < rows[j].hash })
	for _, row := range rows {
		key := normalizeTerm(row.row.Term)
		if row.score <= 1 && !used[key] {
			out = append(out, row.row)
			used[key] = true
		}
		if len(out) >= count {
			break
		}
	}
	return out
}

func spellingSimilarity(a, b string) int {
	score := 0
	for i := 0; i < min(len(a), len(b)); i++ {
		if a[i] != b[i] {
			break
		}
		score++
	}
	for i := 1; i <= min(len(a), len(b)); i++ {
		if a[len(a)-i] != b[len(b)-i] {
			break
		}
		score++
	}
	delta := len(a) - len(b)
	if delta < 0 {
		delta = -delta
	}
	if delta <= 2 {
		score++
	}
	return score
}

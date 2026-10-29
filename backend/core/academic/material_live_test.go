package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Replays the approved private experiment without downloading or importing.
// Network acquisition is exercised separately through OriginalService.
func TestExperimentMaterialQuality(t *testing.T) {
	root := os.Getenv("LAZYMIND_PAPER_EXPERIMENT_DIR")
	if root == "" {
		t.Skip("private paper experiment directory required")
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.json"))
	if err != nil {
		t.Fatal(err)
	}
	var papers []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		DOI     string `json:"doi"`
		ArxivID string `json:"arxiv_id"`
	}
	if err = json.Unmarshal(raw, &papers); err != nil {
		t.Fatal(err)
	}
	blocked := map[string]bool{}
	rows := []map[string]any{}
	for _, paper := range papers {
		raw, err := os.ReadFile(filepath.Join(root, "parsed", paper.ID+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var material struct {
			OK   bool `json:"ok"`
			Data struct {
				Pages []ContentPage `json:"pages"`
			} `json:"data"`
		}
		if err = json.Unmarshal(raw, &material); err != nil {
			t.Fatal(err)
		}
		if !material.OK {
			continue
		}
		q := checkPaperQuality(PaperRef{Title: paper.Title, DOI: paper.DOI, ArxivID: paper.ArxivID}, material.Data.Pages, "external", "experiment-hash")
		blocked[paper.ID] = !q.Eligible
		rows = append(rows, map[string]any{"id": paper.ID, "quality": q})
	}
	if output := os.Getenv("LAZYMIND_PAPER_QUALITY_REPORT"); output != "" {
		if err = os.WriteFile(output, marshal(rows), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"p038", "p043", "p046", "p058", "p061"} {
		if !blocked[id] {
			t.Errorf("known invalid/garbled cached material was accepted: %s", id)
		}
	}
}

// Opt-in fixed-cohort replay through OriginalService and the real current PDF
// reader. Each input has its own deadline; expected inaccessible/invalid papers
// remain report rows rather than being turned into a universal download promise.
func TestExperimentOriginalAcquisition(t *testing.T) {
	root, python := os.Getenv("LAZYMIND_PAPER_EXPERIMENT_DIR"), os.Getenv("LAZYMIND_PAPER_REPLAY_PYTHON")
	if root == "" || python == "" {
		t.Skip("private fixed cohort and current Python reader required")
	}
	paperFixture(t)
	upload := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", upload)
	t.Setenv("LAZYMIND_SHARED_UPLOAD_DIR", upload)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "private-replay-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-LazyMind-Internal-Token") != "private-replay-token" {
			w.WriteHeader(401)
			return
		}
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			w.WriteHeader(400)
			return
		}
		code := `import asyncio,json,sys
from lazymind.chat.api.academic_routes import PDFRequest,parse_pdf
from lazymind.chat.api import academic_routes
academic_routes.require_internal_token=lambda _:None
print(json.dumps(asyncio.run(parse_pdf(PDFRequest(**json.loads(sys.argv[1])),"replay"))))`
		raw, _ := json.Marshal(payload)
		command := exec.CommandContext(r.Context(), python, "-c", code, string(raw))
		command.Env = append(os.Environ(), "PYTHONPATH="+os.Getenv("LAZYMIND_REPLAY_ALGORITHM_ROOT"))
		output, err := command.Output()
		if err != nil {
			w.WriteHeader(422)
			return
		}
		// Runtime logging can precede the final JSON line on stdout.
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var parsed any
		if json.Unmarshal([]byte(lines[len(lines)-1]), &parsed) != nil {
			w.WriteHeader(422)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(parsed)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	raw, err := os.ReadFile(filepath.Join(root, "pool.json"))
	if err != nil {
		t.Fatal(err)
	}
	var papers []struct {
		ID, Title, DOI string
		ArxivID        string                                `json:"arxiv_id"`
		Candidates     []struct{ URL, Kind, Version string } `json:"pdf_candidates"`
	}
	if err = json.Unmarshal(raw, &papers); err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, len(papers))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, paper := range papers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			started := time.Now()
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			row := map[string]any{"id": paper.ID, "downloaded": false, "parsed": false, "eligible": false}
			rows[i] = row
			candidates := []FulltextCandidate{}
			for _, c := range paper.Candidates {
				provider := "experiment"
				if c.Kind == "arxiv" {
					provider = "arxiv"
				}
				candidates = append(candidates, FulltextCandidate{URL: c.URL, Version: c.Version, SourceProvider: provider, SourceType: "open_access_pdf"})
			}
			// Include the previously discovered public PDF even if it originated from a
			// general-search lead, without trusting that lead's identity.
			if b, e := os.ReadFile(filepath.Join(root, "pdf", paper.ID+"-result.json")); e == nil {
				var old map[string]any
				_ = json.Unmarshal(b, &old)
				if attempts, ok := old["attempts"].([]any); ok {
					for _, item := range attempts {
						attempt, _ := item.(map[string]any)
						address, _ := attempt["url"].(string)
						if address != "" {
							candidates = append(candidates, FulltextCandidate{URL: address, SourceProvider: "experiment", SourceType: "open_access_pdf"})
						}
					}
				}
			}
			attempts := []map[string]any{}
			for _, candidate := range candidates {
				if ctx.Err() != nil {
					break
				}
				attempt := map[string]any{"url": candidate.URL}
				attempts = append(attempts, attempt)
				path, _, e := acquirePDF(ctx, "replay", candidate)
				if e != nil {
					attempt["error"] = e.Error()
					continue
				}
				row["downloaded"] = true
				pages, hash, e := parsePaperPDF(ctx, path, "")
				os.Remove(path)
				row["pdf_hash"] = hash
				if e != nil {
					attempt["error"] = e.Error()
					continue
				}
				row["parsed"] = true
				if output := os.Getenv("LAZYMIND_PAPER_ACQUISITION_REPORT"); output != "" {
					folder := filepath.Join(filepath.Dir(output), "original-parsed")
					if e := os.MkdirAll(folder, 0700); e == nil {
						_ = os.WriteFile(filepath.Join(folder, paper.ID+".json"), marshal(map[string]any{"pages": pages, "pdf_hash": hash}), 0600)
					}
				}
				q := checkPaperQuality(PaperRef{Title: paper.Title, DOI: paper.DOI, ArxivID: paper.ArxivID}, pages, "external", hash)
				row["quality"] = q
				row["eligible"] = q.Eligible
				row["pages"] = len(pages)
				if q.Eligible {
					break
				}
			}
			row["attempts"], row["elapsed_ms"] = attempts, time.Since(started).Milliseconds()
		}()
	}
	wg.Wait()
	output := os.Getenv("LAZYMIND_PAPER_ACQUISITION_REPORT")
	if output == "" {
		t.Fatal("private report path required")
	}
	if err = os.WriteFile(output, marshal(rows), 0600); err != nil {
		t.Fatal(err)
	}
	downloads, parsed, eligible := 0, 0, 0
	for _, row := range rows {
		if row["downloaded"] == true {
			downloads++
		}
		if row["parsed"] == true {
			parsed++
		}
		if row["eligible"] == true {
			eligible++
		}
	}
	fmt.Printf("fixed cohort=%d downloads=%d parsed=%d verified=%d\n", len(rows), downloads, parsed, eligible)
	if downloads == 0 || parsed == 0 {
		t.Fatal("no actual acquisition/reader path succeeded")
	}
}

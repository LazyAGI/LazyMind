package academic

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/artifact"
	"lazymind/core/asyncjob"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

func TestConversationPaperOriginalSurvivesArxivFailureAndKeepsItsHash(t *testing.T) {
	db, b, item, svc := importPipelineFixture(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}); err != nil {
		t.Fatal(err)
	}
	owner := orm.BaseModel{CreateUserID: "alice"}
	title := "DeepSeek-V4.1-Flash: Pushing the Limits of KV Cache Compression"
	opening := title + "\nDeepSeek-AI\nAbstract\n" + strings.Repeat("We present our model and evaluate its long context behavior. ", 4)
	body := []byte("%PDF-1.7\nfixture original\n%%EOF")
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	t.Setenv("LAZYMIND_AGENTIC_WORKSPACE", t.TempDir())
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://parser.test")
	root := artifact.ConversationAgentWorkspaceRoots("alice", "chat")[0]
	fileID := "fr_123456abcdef"
	dir := filepath.Join(root, "file-resources", fileID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := conversationPDFManifest{FileID: fileID, SHA256: digest, Source: "web", ParseStatus: "ready", URL: "https://huggingface.co/deepseek-ai/report.pdf?signature=private"}
	for name, data := range map[string][]byte{"manifest.json": marshal(manifest), "original.pdf": body, "parsed.md": []byte("<!-- page:1 -->\n" + opening)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	b.EntryType = "conversation_sources"
	b.PolicySnapshotJSON = marshal(sourceImportPolicy{Context: SourceContext{ConversationID: "chat", HistoryID: "answer"}})
	for _, row := range []any{&orm.Conversation{ID: "chat", BaseModel: owner}, &orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: marshal(map[string]any{"sources": []sourceRecord{{ID: "55.1", Title: "[2609.19969] " + title, URL: "https://arxiv.org/abs/2609.19969"}}})}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Save(&b).Error; err != nil {
		t.Fatal(err)
	}
	arxivRequests := 0
	mockPaperTransport(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "parser.test":
			return paperTestResponse(r, 200, string(marshal(map[string]any{"pages": []ContentPage{{Page: 1, Text: opening}}, "parser_version": orm.AcademicPaperParserVersion}))), nil
		case "scan.test":
			return paperTestResponse(r, 200, `{"items":[{"dataset_id":"target","exists":false,"allowed":true}],"source_map":{}}`), nil
		case "export.arxiv.org":
			arxivRequests++
			return paperTestResponse(r, 429, "limited"), nil
		}
		return nil, fmt.Errorf("unexpected request to %s", r.URL.Host)
	})
	req := capability.PaperSelection{Title: title, Authors: []string{"DeepSeek-AI"}, ArxivID: "2609.19969"}
	prepare := func(row orm.PaperImportItem, p capability.PaperSelection) orm.PaperImportItem {
		row.AcademicWorkID = ""
		row.SelectedCandidateJSON = marshal(savedImportCandidate{Requested: &p})
		return row
	}
	item = prepare(item, req)
	if err := db.Save(&item).Error; err != nil {
		t.Fatal(err)
	}
	job := asyncjob.Job{ID: "job", AttemptCount: 1}
	if err := processImportItem(t.Context(), job, b, item, sourceImportCaller(b), svc); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var link orm.AcademicWorkDocument
	if err := db.Where("document_id=?", item.DocumentID).Take(&link).Error; err != nil || link.ContentSHA256 != digest || link.SourceVersion != "" ||
		link.SourceProvider != "web" || strings.Contains(link.SourceLocator, "signature") || arxivRequests != 0 {
		t.Fatalf("link=%+v arXiv=%d err=%v", link, arxivRequests, err)
	}
	req.ExactVersion, req.ArxivID = true, "2609.19969v2"
	pinned := prepare(orm.PaperImportItem{ID: "pinned", BatchID: b.ID, Status: "pending", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]string{}), ErrorDetailsJSON: marshal(map[string]string{})}, req)
	if err := db.Create(&pinned).Error; err != nil {
		t.Fatal(err)
	}
	if err := processImportItem(t.Context(), job, b, pinned, sourceImportCaller(b), svc); err == nil || arxivRequests != 1 {
		t.Fatalf("unverified version accepted: arXiv=%d err=%v", arxivRequests, err)
	}
	var verified savedImportCandidate
	if err := json.Unmarshal(item.SelectedCandidateJSON, &verified); err != nil || verified.ConversationOriginal == nil {
		t.Fatalf("missing verified original snapshot: %v", err)
	}
	for _, changed := range [][]byte{[]byte("%PDF-1.7\nreplacement original\n%%EOF"), []byte("<html>not a PDF</html>")} {
		if err := os.WriteFile(filepath.Join(dir, "original.pdf"), changed, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := acquireSavedConversationOriginal(t.Context(), svc, sourceImportCaller(b), b, verified); err == nil {
			t.Fatal("accepted bytes that replaced the verified conversation PDF")
		}
	}
	// The worker must not trust the same manifest outside the authorized chat.
	b.CreatedBy = "mallory"
	roots, err := sourceConversationRoots(t.Context(), b)
	if err != nil || len(roots) != 0 {
		t.Fatalf("foreign roots: %v %v", roots, err)
	}
	if paperResourceOpeningMatches(PaperRef{Title: "Cross Layer Attention", Authors: []string{"Brandon"}}, opening+"\n<!-- page:2 -->\nCross Layer Attention Brandon", "") {
		t.Fatal("cited paper incorrectly accepted as original")
	}
}

func TestArxivRetrySchedulesOnlyDeferredItemsAndHonorsCooldown(t *testing.T) {
	db, b, item, _ := importPipelineFixture(t)
	t.Setenv("LAZYMIND_AGENTIC_WORKSPACE", "")
	st, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	old := store.State()
	store.Init(db, db, st)
	t.Cleanup(func() { store.Init(db, db, old); _ = st.Close() })
	b.EntryType = "conversation_sources"
	if err = db.Save(&b).Error; err != nil {
		t.Fatal(err)
	}
	item.SelectedCandidateJSON = marshal(savedImportCandidate{FulltextCandidate: testPDFCandidate()})
	if err = db.Save(&item).Error; err != nil {
		t.Fatal(err)
	}
	other := item
	other.ID, other.Status, other.Stage = "completed", "succeeded", "processing"
	if err = db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	mockPaperTransport(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "scan.test":
			return paperTestResponse(r, 200, `{"items":[{"dataset_id":"target","exists":false,"allowed":true}]}`), nil
		case "export.arxiv.org":
			requests.Add(1)
		default:
			return nil, fmt.Errorf("unexpected URL %s", r.URL)
		}
		res := paperTestResponse(r, 429, "limited")
		res.Header.Set("Retry-After", "65")
		return res, nil
	})
	start := time.Now()
	if _, err = handleImportBatchJob(t.Context(), asyncjob.Job{ID: "job", AttemptCount: 1}, importErrorReporter{}, b, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("id=?", b.ID).Take(&b).Error; err != nil {
		t.Fatal(err)
	}
	var retry orm.AsyncJob
	if err = db.Where("id=?", b.AsyncJobID).Take(&retry).Error; err != nil {
		t.Fatal(err)
	}
	var payload paperImportPayload
	if json.Unmarshal(retry.PayloadJSON, &payload) != nil || len(payload.ItemIDs) != 1 || payload.ItemIDs[0] != item.ID || retry.NextRunAt.Before(start.Add(64*time.Second)) {
		t.Fatalf("wrong retry: %+v %+v", retry, payload)
	}
	_, err = lookupImportArxiv(t.Context(), "2607.05147")
	if err == nil || requests.Load() != 1 {
		t.Fatalf("cooldown ignored: requests=%d err=%v", requests.Load(), err)
	}
	if err := st.Del(t.Context(), "academic/search/cooldown/"+sourceHash(map[string]any{"source": "arxiv", "key": "", "tools": map[string]any(nil)})); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&retry).Updates(map[string]any{"status": "running", "attempt_count": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := handleImportBatchJob(t.Context(), asyncjob.Job{ID: retry.ID, AttemptCount: 1}, importErrorReporter{}, b, payload.ItemIDs); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", other.ID).Take(&other).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "failed" || item.ErrorCode != "provider_rate_limited" || item.AttemptCount != 2 || other.Status != "succeeded" || requests.Load() != 2 {
		t.Fatalf("retry did not preserve receipts: failed=%s/%s/%d completed=%s requests=%d", item.Status, item.ErrorCode, item.AttemptCount, other.Status, requests.Load())
	}
}

func TestArxiv429WithoutHeaderUsesSharedDefaultCooldown(t *testing.T) {
	paperFixture(t)
	start := time.Now()
	attempts := 0
	call := func() (string, error) {
		attempts++
		return "", paperPDFHTTPError{status: 429}
	}
	_, err := withArxivSource(t.Context(), call)
	var failure *searchFailure
	if !errors.As(err, &failure) || failure.RetryAt == nil || failure.RetryAt.Before(start.Add(29*time.Second)) || failure.RetryAt.After(start.Add(31*time.Second)) {
		t.Fatalf("default cooldown: %+v", err)
	}
	_, err = withArxivSource(t.Context(), call)
	if err == nil || attempts != 1 {
		t.Fatalf("cooldown ignored: attempts=%d err=%v", attempts, err)
	}
}

func TestTitleSelectionDistinguishesConferenceEditionAndAmbiguousWorks(t *testing.T) {
	paperFixture(t)
	title := "Reducing Transformer Key-Value Cache Size with Cross-Layer Attention"
	const published = "10.52202/079017-2758"
	const preprint = "10.48550/arxiv.2405.12981"
	row := `{"title":%q,"publication_year":2024,"doi":%q,"authorships":[{"author":{"display_name":"William Brandon"}}],"primary_location":{"source":{"display_name":%q}}}`
	mockPaperTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "api.openalex.org" {
			return nil, fmt.Errorf("unexpected URL %s", r.URL)
		}
		return paperTestResponse(r, 200, `{"results":[`+fmt.Sprintf(row, title, published, "neural information processing systems")+`,`+fmt.Sprintf(row, title, preprint, "arXiv")+`]}`), nil
	})
	p := capability.PaperSelection{Title: title, Authors: []string{"Brandon"}, Year: 2024, Venue: "NeurIPS 2024"}
	work, err := resolveSelectedTitle(t.Context(), "alice", p)
	if err != nil || work.DOI != published {
		t.Fatalf("publication: %+v %v", work, err)
	}
	p.Venue = ""
	if _, err := resolveSelectedTitle(t.Context(), "alice", p); !errors.Is(err, errPaperSelectionUnconfirmed) {
		t.Fatalf("ambiguous title: %v", err)
	}
}

func mockPaperTransport(t *testing.T, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = sourceTransport(fn)
	t.Cleanup(func() { http.DefaultTransport = previous })
}
func paperTestResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

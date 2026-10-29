package vocabulary

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type cloudRoundTrip func(*http.Request) (*http.Response, error)

func (f cloudRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func cloudResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func cloudTestClient(t *testing.T, provider string, f cloudRoundTrip) *cloudClient {
	t.Helper()
	c, e := newCloudClient(provider, "secret")
	if e != nil {
		t.Fatal(e)
	}
	c.limiter = nil
	c.httpClient.Transport = f
	return c
}
func TestCloudEudicPreservesStarsOnRetry(t *testing.T) {
	calls := 0
	c := cloudTestClient(t, "eudic", func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.frdic.com/api/open/v1/studylist/words" || r.Header.Get("Authorization") != "secret" {
			t.Fatal("wrong request", r.URL)
		}
		var p map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&p)
		if string(p["words"]) != "[\"apple\"]" || p["items"] != nil || string(p["category_id"]) != "\"42\"" {
			t.Fatal("would overwrite star", p)
		}
		if calls == 1 {
			return cloudResponse(500, `{"message":"secret"}`), nil
		}
		return cloudResponse(201, `{"message":"单词导入成功,导入数量 : 1"}`), nil
	})
	if err := c.AddWord(context.Background(), "42", "apple", false); err == nil {
		t.Fatal("partial failure hidden")
	}
	if err := c.AddWord(context.Background(), "42", "apple", false); err != nil {
		t.Fatal(err)
	}
}
func TestCloudMaiMemoPreservesContentAndMetadata(t *testing.T) {
	writes := 0
	content := "# Chapter\npear"
	c := cloudTestClient(t, "maimemo", func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "open.maimemo.com" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("wrong origin/auth")
		}
		if r.Method == "POST" {
			writes++
			var p struct{ Notepad map[string]any }
			json.NewDecoder(r.Body).Decode(&p)
			if p.Notepad["title"] != "Mine" || p.Notepad["brief"] != "Keep" || p.Notepad["status"] != "UNPUBLISHED" || p.Notepad["content"] != "# Chapter\npear\napple" || len(p.Notepad["tags"].([]any)) != 1 {
				t.Fatalf("metadata lost: %#v", p)
			}
			content = p.Notepad["content"].(string)
		}
		b, _ := json.Marshal(map[string]any{"success": true, "errors": []any{}, "data": map[string]any{"notepad": map[string]any{"id": "42", "title": "Mine", "brief": "Keep", "status": "UNPUBLISHED", "tags": []string{"tag"}, "content": content}}})
		return cloudResponse(200, string(b)), nil
	})
	for i := 0; i < 2; i++ {
		if e := c.AddWord(context.Background(), "42", "apple", false); e != nil {
			t.Fatal(e)
		}
	}
	if writes != 1 {
		t.Fatal("duplicate append", writes)
	}
}
func TestCloudMaiMemoStudyZeroCountRequiresExistingRecord(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_added", true: "already_added"}[exists], func(t *testing.T) {
			c := cloudTestClient(t, "maimemo", func(r *http.Request) (*http.Response, error) {
				var data string
				switch {
				case strings.Contains(r.URL.Path, "notepads"):
					data = `{"notepad":{"id":"42","title":"Mine","brief":"","status":"UNPUBLISHED","tags":[],"content":"apple"}}`
				case strings.HasSuffix(r.URL.Path, "vocabulary"):
					data = `{"voc":{"id":"v1","spelling":"apple"}}`
				case strings.HasSuffix(r.URL.Path, "add_words"):
					data = `{"added_count":0}`
				case strings.HasSuffix(r.URL.Path, "query_study_records"):
					data = `{"records":[],"count":0}`
					if exists {
						data = `{"records":[{"voc_id":"v1"}],"count":0}`
					}
				default:
					t.Fatal(r.URL)
				}
				return cloudResponse(200, `{"success":true,"data":`+data+`}`), nil
			})
			err := c.AddWord(context.Background(), "42", "apple", true)
			if (err == nil) != exists {
				t.Fatalf("exists %v error %v", exists, err)
			}
		})
	}
}
func TestCloudErrorsAreSafeAndBounded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		retry  bool
	}{{"unauthorized", 401, `secret`, false}, {"rate", 429, `secret`, true}, {"api", 200, `{"success":false,"errors":["secret"]}`, false}, {"embedded", 200, `{"success":true,"errors":["secret"],"data":{"notepads":[]}}`, false}, {"malformed", 200, `{`, false}, {"oversize", 200, strings.Repeat("x", 2*1024*1024+1), false}} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudTestClient(t, "maimemo", func(r *http.Request) (*http.Response, error) {
				res := cloudResponse(tc.status, tc.body)
				res.Header.Set("Retry-After", "120")
				return res, nil
			})
			_, err := c.ListWordbooks(context.Background())
			var safe *CloudClientError
			if !errors.As(err, &safe) || strings.Contains(err.Error(), "secret") || safe.Retryable != tc.retry {
				t.Fatalf("unsafe/wrong error %v", err)
			}
			if tc.status == 429 && safe.RetryAfter != 120*time.Second {
				t.Fatal(safe)
			}
		})
	}
}
func TestCloudListAndCreate(t *testing.T) {
	for _, provider := range []string{"eudic", "maimemo"} {
		t.Run(provider, func(t *testing.T) {
			c := cloudTestClient(t, provider, func(r *http.Request) (*http.Response, error) {
				if provider == "eudic" {
					if r.Method == "GET" {
						return cloudResponse(200, `{"data":[{"id":"42","name":"Mine"}]}`), nil
					}
					return cloudResponse(201, `{"data":{"id":"42","name":"Mine"}}`), nil
				}
				if r.Method == "GET" {
					return cloudResponse(200, `{"success":true,"data":{"notepads":[{"id":"42","title":"Mine"}]}}`), nil
				}
				var p struct{ Notepad map[string]any }
				json.NewDecoder(r.Body).Decode(&p)
				if p.Notepad["status"] != "UNPUBLISHED" || p.Notepad["content"] != "" {
					t.Fatal("publishes invented content", p)
				}
				return cloudResponse(200, `{"success":true,"data":{"notepad":{"id":"42","title":"Mine"}}}`), nil
			})
			list, e := c.ListWordbooks(context.Background())
			if e != nil || len(list) != 1 || list[0].Name != "Mine" {
				t.Fatal(list, e)
			}
			book, e := c.CreateWordbook(context.Background(), "Mine")
			if e != nil || book.ID != "42" {
				t.Fatal(book, e)
			}
		})
	}
}
func TestCloudRejectRedirect(t *testing.T) {
	calls := 0
	c := cloudTestClient(t, "eudic", func(r *http.Request) (*http.Response, error) {
		calls++
		res := cloudResponse(302, `{}`)
		res.Header.Set("Location", "https://evil.invalid/")
		return res, nil
	})
	_, e := c.ListWordbooks(context.Background())
	if e == nil || calls != 1 {
		t.Fatal("redirect followed", e, calls)
	}
}

func TestCloudRejectsIncompleteNotepadBeforeOverwrite(t *testing.T) {
	writes := 0
	c := cloudTestClient(t, "maimemo", func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			writes++
		}
		return cloudResponse(200, `{"success":true,"data":{"notepad":{"id":"42","status":"UNPUBLISHED","title":"Mine","tags":[]}}}`), nil
	})
	if err := c.AddWord(context.Background(), "42", "apple", false); err == nil || writes != 0 {
		t.Fatalf("incomplete remote content overwritten: %v, writes %d", err, writes)
	}
}
func TestCloudRejectsUnexpectedEudicWriteStatus(t *testing.T) {
	c := cloudTestClient(t, "eudic", func(r *http.Request) (*http.Response, error) { return cloudResponse(200, `{"message":"failure"}`), nil })
	if c.AddWord(context.Background(), "42", "apple", false) == nil {
		t.Fatal("non-success mutation accepted")
	}
}
func TestCloudRateLimitSharedAndCancellation(t *testing.T) {
	a, _ := newCloudClient("eudic", "unique-rate-test")
	b, _ := newCloudClient("eudic", "unique-rate-test")
	if a.limiter != b.limiter {
		t.Fatal("limiter is not shared")
	}
	a.limiter.cool(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.limiter.wait(ctx) == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestCloudLongCooldownReturnsTypedDeferral(t *testing.T) {
	c := cloudTestClient(t, "eudic", func(*http.Request) (*http.Response, error) { t.Fatal("request during cooldown"); return nil, nil })
	c.limiter = &cloudRateLimit{next: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.ListWordbooks(ctx)
	var safe *CloudClientError
	if !errors.As(err, &safe) || safe.Code != "rate_limited" || !safe.Retryable || safe.RetryAfter < 59*time.Minute {
		t.Fatalf("cooldown lost: %#v", err)
	}
}

func TestCloudConcurrentAccountAppendsPreserveBothWords(t *testing.T) {
	var mu sync.Mutex
	content := "pear"
	gets := 0
	firstRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	transport := cloudRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			mu.Lock()
			snapshot := content
			gets++
			first := gets == 1
			mu.Unlock()
			if first {
				close(firstRead)
				<-releaseFirst
			}
			b, _ := json.Marshal(map[string]any{"notepad": map[string]any{"id": "42", "status": "UNPUBLISHED", "title": "Mine", "brief": "", "tags": []string{}, "content": snapshot}})
			return cloudResponse(200, string(b)), nil
		}
		var p struct {
			Notepad struct {
				Content string `json:"content"`
			} `json:"notepad"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		content = p.Notepad.Content
		mu.Unlock()
		return cloudResponse(200, `{"notepad":{"id":"42"}}`), nil
	})
	a, _ := newCloudClient("maimemo", "concurrent-account-test")
	b, _ := newCloudClient("maimemo", "concurrent-account-test")
	a.limiter.interval = 0
	a.httpClient.Transport = transport
	b.httpClient.Transport = transport
	done := make(chan error, 2)
	go func() { done <- a.AddWord(context.Background(), "42", "apple", false) }()
	<-firstRead
	go func() { done <- b.AddWord(context.Background(), "42", "banana", false) }()
	// The second call must not read a stale snapshot while the first merge owns the account.
	time.Sleep(20 * time.Millisecond)
	close(releaseFirst)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(content, "apple") || !strings.Contains(content, "banana") {
		t.Fatalf("concurrent append lost: %q", content)
	}
}

func TestCloudMaiMemoWordbookPageLimit(t *testing.T) {
	for _, total := range []int{0, 10, 13} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			calls := 0
			c := cloudTestClient(t, "maimemo", func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("limit") != "10" {
					t.Fatal("MaiMemo rejects limits above 10")
				}
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil || offset != calls*10 {
					t.Fatal("incorrect pagination offset", offset)
				}
				calls++
				rows := []map[string]string{}
				for i := offset; i < total && i < offset+10; i++ {
					rows = append(rows, map[string]string{"id": strconv.Itoa(i), "title": "Book"})
				}
				b, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"notepads": rows}})
				return cloudResponse(200, string(b)), nil
			})
			books, err := c.ListWordbooks(context.Background())
			if err != nil || len(books) != total || calls != total/10+1 {
				t.Fatalf("books=%d calls=%d error=%v", len(books), calls, err)
			}
			for i, b := range books {
				if b.ID != strconv.Itoa(i) {
					t.Fatal("missing or duplicated wordbook", i)
				}
			}
		})
	}
}

func TestCloudEudicRejectsMalformedWordbooks(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		create     bool
	}{
		{"null_list", `{"data":null}`, false},
		{"null_entry", `{"data":[null]}`, false},
		{"missing_id", `{"data":[{"name":"Mine"}]}`, false},
		{"blank_id", `{"data":[{"id":" ","name":"Mine"}]}`, false},
		{"missing_name", `{"data":[{"id":"42"}]}`, false},
		{"duplicate_id", `{"data":[{"id":"42","name":"One"},{"id":"42","name":"Two"}]}`, false},
		{"blank_created_id", `{"data":{"id":" ","name":"Mine"}}`, true},
		{"missing_created_name", `{"data":{"id":"42"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudTestClient(t, "eudic", func(*http.Request) (*http.Response, error) {
				status := 200
				if tc.create {
					status = 201
				}
				return cloudResponse(status, tc.body), nil
			})
			var err error
			if tc.create {
				_, err = c.CreateWordbook(context.Background(), "Mine")
			} else {
				_, err = c.ListWordbooks(context.Background())
			}
			var safe *CloudClientError
			if !errors.As(err, &safe) || safe.Code != "invalid_response" {
				t.Fatalf("accepted malformed response: %v", err)
			}
		})
	}
}
func TestCloudEudicListRequiresGETSuccessStatus(t *testing.T) {
	c := cloudTestClient(t, "eudic", func(*http.Request) (*http.Response, error) { return cloudResponse(201, `{"data":[]}`), nil })
	if _, e := c.ListWordbooks(context.Background()); e == nil {
		t.Fatal("unexpected GET status accepted")
	}
}
func TestCloudEudicOfficialAuthAndDefaultWordbook(t *testing.T) {
	c, _ := newCloudClient("eudic", "NIS complete-token")
	c.limiter = nil
	c.httpClient.Transport = cloudRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "NIS complete-token" || r.URL.Query().Get("language") != "en" {
			t.Fatal("authorization or language modified")
		}
		return cloudResponse(200, `{"data":[{"id":"0","language":"en","name":"我的生词本"}],"message":""}`), nil
	})
	books, e := c.ListWordbooks(context.Background())
	if e != nil || len(books) != 1 || books[0].ID != "0" {
		t.Fatal(books, e)
	}
}
func TestCloudEudicAuthAndRateErrors(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c := cloudTestClient(t, "eudic", func(*http.Request) (*http.Response, error) {
				return cloudResponse(status, `{"message":"private upstream details"}`), nil
			})
			_, e := c.ListWordbooks(context.Background())
			var safe *CloudClientError
			if !errors.As(e, &safe) {
				t.Fatal(e)
			}
			if status == 401 && (safe.Code != "invalid_token" || safe.Retryable) {
				t.Fatal(safe)
			}
			if status == 403 && (safe.Code != "rate_limited" || !safe.Retryable || safe.RetryAfter < time.Hour) {
				t.Fatal(safe)
			}
		})
	}
}
func TestCloudEudicThrottleRespectsBothPublishedWindows(t *testing.T) {
	c, _ := newCloudClient("eudic", "published-rate-window-test")
	for _, window := range []struct {
		duration time.Duration
		max      int
	}{{time.Minute, 30}, {30 * time.Minute, 500}} {
		possible := int(window.duration/c.limiter.interval) + 1
		if possible > window.max {
			t.Fatalf("allows %d requests in %v (limit %d)", possible, window.duration, window.max)
		}
	}
}

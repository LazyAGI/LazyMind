package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/doc"
	"lazymind/core/resourceupdate"
	"lazymind/core/scheduler"
	"lazymind/core/state"
	"lazymind/core/taskcenter"
)

type desktopUpdateParticipant struct {
	pause  func(uint64) (*maintenancegate.Ticket, error)
	resume func(uint64) error
}

// This lease only closes admissions during the short update countdown. It is
// not a task cancellation API. Losing the desktop controller reopens admission.
type desktopUpdateControl struct {
	mu                                                       sync.Mutex
	bootstrapTx, bootstrapOwner, completedTx, completedOwner string
	onBootstrapRelease                                       func() error
	bootstrapTickets                                         []*maintenancegate.Ticket
	bootstrapResumes                                         []func(string) error
	token                                                    string
	epoch, highest, renewal                                  uint64
	active                                                   int64
	timer                                                    *time.Timer
	participants                                             []desktopUpdateParticipant
	tickets                                                  []*maintenancegate.Ticket
	count                                                    func(context.Context) (int64, error)
}

func (c *desktopUpdateControl) resumeLocked() error {
	var result error
	for _, p := range c.participants[:len(c.tickets)] {
		result = errors.Join(result, p.resume(c.epoch))
	}
	if result == nil {
		c.epoch = 0
		c.tickets = nil
	}
	return result
}

func (c *desktopUpdateControl) control(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if r.Method != http.MethodPost || err != nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || c.token == "" || subtle.ConstantTimeCompare([]byte(c.token), []byte(r.Header.Get("X-LazyMind-Internal-Token"))) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var request struct {
		Action        string `json:"action"`
		TransactionID string `json:"transactionId,omitempty"`
		Owner         string `json:"owner,omitempty"`
		Epoch         uint64 `json:"epoch"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF || (request.Action != "status" && request.Action != "pause" && request.Action != "resume" && request.Action != "bootstrap-complete") || (request.Action != "status" && request.Action != "bootstrap-complete" && request.Epoch == 0) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.Action == "bootstrap-complete" {
		if c.CompleteUpdateBootstrap(request.TransactionID, request.Owner) != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		request.Action = "status"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if request.Action == "pause" {
		if (c.epoch != 0 && c.epoch != request.Epoch) || (c.epoch == 0 && request.Epoch <= c.highest) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if c.epoch == 0 {
			c.epoch = request.Epoch
			c.highest = request.Epoch
			for _, p := range c.participants {
				ticket, err := p.pause(c.epoch)
				if err != nil {
					_ = c.resumeLocked()
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				c.tickets = append(c.tickets, ticket)
			}
		}
		if c.timer != nil {
			c.timer.Stop()
		}
		c.renewal++
		epoch, renewal := c.epoch, c.renewal
		c.timer = time.AfterFunc(2*time.Minute, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.epoch == epoch && c.renewal == renewal {
				_ = c.resumeLocked()
			}
		})
	}
	if request.Action == "resume" {
		if c.epoch != 0 && c.epoch != request.Epoch {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if c.epoch != 0 && c.resumeLocked() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if c.timer != nil {
			c.timer.Stop()
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	n, err := c.count(ctx)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	n += c.active
	for _, ticket := range c.tickets {
		if err := ticket.Validate(); err == maintenancegate.ErrActive {
			n++
		} else if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	wire := map[string]any{"active": n + 0, "paused": c.epoch != 0, "epoch": c.epoch}
	if c.bootstrapTx != "" {
		wire["bootstrap"] = map[string]any{"transactionId": c.bootstrapTx, "owner": c.bootstrapOwner, "sealed": true}
	}
	_ = json.NewEncoder(w).Encode(wire)
}

func (c *desktopUpdateControl) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/desktop-update" {
			c.control(w, r)
			return
		}
		// GET event subscriptions are observers. WebSocket sessions and all
		// mutating requests are work; ResponseWriter is passed through intact.
		websocketWork := strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && r.URL.Path != "/realtime/connect" && r.URL.Path != "/browser/extension/connect"
		work := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions || websocketWork
		if work {
			countRequest := true
			if route := mux.CurrentRoute(r); route != nil && r.Method == http.MethodPost {
				template, _ := route.GetPathTemplate()
				// Idle claims wait for work. Pending/running ExternalChatRun rows
				// remain counted across a claim; new polls still obey the pause gate.
				countRequest = template != "/external-chat/hosts/{provider}/claim"
			}
			c.mu.Lock()
			if c.epoch != 0 || c.bootstrapTx != "" {
				c.mu.Unlock()
				common.ReplyErr(w, "desktop update is preparing", http.StatusServiceUnavailable)
				return
			}
			if countRequest {
				c.active++
			}
			c.mu.Unlock()
			if countRequest {
				defer func() { c.mu.Lock(); c.active--; c.mu.Unlock() }()
			}
		}
		next.ServeHTTP(w, r)
	})
}

func desktopUpdateTaskCount(ctx context.Context, db, lazy *gorm.DB, ragEnabled bool) (int64, error) {
	if db == nil {
		return 0, errors.New("task state unavailable")
	}
	total, err := taskcenter.ActiveForDesktopUpdate(ctx, db)
	if err != nil {
		return 0, err
	}
	resources, err := resourceupdate.ActiveForUpdate(ctx, db)
	if err != nil {
		return 0, err
	}
	total += resources
	queries := []*gorm.DB{

		db.Model(&orm.AsyncJob{}).Where("status IN ?", []string{"pending", "running"}),
		db.Model(&orm.SubAgentTask{}).Where("status IN ?", []string{"pending", "running"}),
		db.Model(&orm.ExternalChatRun{}).Where("status IN ?", []string{"pending", "running"}),
		db.Model(&orm.ChatHistory{}).Where("run_status = ?", "generating"),
		db.Model(&orm.MultiAnswersChatHistory{}).Where("run_status = ?", "generating"),
		db.Table("plugin_session_steps").Where("(validity = ? OR validity = '') AND status IN ?", "effective", []string{"queued", "claimed", "running"}),
	}
	for _, q := range queries {
		var n int64
		if err := q.WithContext(ctx).Count(&n).Error; err != nil {
			return 0, err
		}
		total += n
	}
	// Locally submitted work includes the window before a LazyLLM row exists.
	var tasks []orm.Task
	if err := db.WithContext(ctx).Select("id, lazyllm_task_id, ext").Where("deleted_at IS NULL").Find(&tasks).Error; err != nil {
		return 0, err
	}
	remoteIDs := []string{}
	for _, task := range tasks {
		if task.LazyllmTaskID != "" {
			if ragEnabled {
				remoteIDs = append(remoteIDs, task.LazyllmTaskID)
			}
			continue
		}
		var ext struct {
			State string `json:"task_state"`
			Level string `json:"processing_level"`
		}
		if len(task.Ext) == 0 {
			continue
		}
		if json.Unmarshal(task.Ext, &ext) != nil {
			return 0, errors.New("task state invalid")
		}
		if ext.Level == "stored" || ext.State == "" {
			continue
		}
		switch strings.ToUpper(ext.State) {
		case "SUCCESS", "SUCCEEDED", "FAILED", "CANCELED", "CANCELLED", "SUSPENDED":
		default:
			total++
		}
	}
	if ragEnabled {
		if lazy == nil {
			return 0, errors.New("document state unavailable")
		}
		var n int64
		if err := lazy.WithContext(ctx).Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("UPPER(status) NOT IN ?", []string{"SUCCESS", "SUCCEEDED", "FAILED", "CANCELED", "CANCELLED", "SUSPENDED"}).Count(&n).Error; err != nil {
			return 0, err
		}
		total += n
		for start := 0; start < len(remoteIDs); start += 500 {
			ids := remoteIDs[start:min(start+500, len(remoteIDs))]
			var found []string
			if err := lazy.WithContext(ctx).Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("task_id IN ?", ids).Distinct("task_id").Pluck("task_id", &found).Error; err != nil {
				return 0, err
			}
			known := map[string]bool{}
			for _, id := range found {
				known[id] = true
			}
			for _, id := range ids {
				if !known[id] {
					total++
				}
			}
		}
	}
	return total, nil
}

// Register each background owner at construction so a new runner cannot be
// omitted from the update pause/resume lifecycle.
func (c *desktopUpdateControl) startRunner(ctx context.Context, db *gorm.DB, options asyncjob.Options) *asyncjob.Runner {
	if c.bootstrapTx != "" {
		options.BootstrapKey = c.bootstrapTx + "\x00" + c.bootstrapOwner
	}
	r := asyncjob.Start(ctx, db, options)
	if r.BootstrapTicket() != nil {
		c.bootstrapTickets = append(c.bootstrapTickets, r.BootstrapTicket())
		c.bootstrapResumes = append(c.bootstrapResumes, r.UnsealBootstrap)
	}
	c.participants = append(c.participants, desktopUpdateParticipant{r.PauseForUpdate, r.ResumeFromUpdate})
	return r
}

// Register before starting either the full runtime or its independent idle path.
func (c *desktopUpdateControl) startResourceUpdates(ctx context.Context, db *gorm.DB, store state.Store, cfg resourceupdate.Config, enabled bool) []<-chan struct{} {
	c.participants = append(c.participants, desktopUpdateParticipant{resourceupdate.PauseForUpdate, resourceupdate.ResumeFromUpdate})

	done := []<-chan struct{}{resourceupdate.StartIdle(ctx, db, store, cfg, enabled)}
	if enabled {
		done = append(done, resourceupdate.Start(ctx, db, store, cfg))
	}
	return done
}

func (c *desktopUpdateControl) BeginUpdateBootstrap(tx, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(tx) != 32 || owner == "" {
		return maintenancegate.ErrInvalidEpoch
	}
	if c.bootstrapTx != "" {
		if c.bootstrapTx == tx && c.bootstrapOwner == owner {
			return nil
		}
		return maintenancegate.ErrConflict
	}
	key := tx + "\x00" + owner
	owners := []struct {
		seal   func(string) (*maintenancegate.Ticket, error)
		resume func(string) error
	}{
		{scheduler.SealForBootstrap, scheduler.UnsealBootstrap}, {doc.SealDeferredIndexingForBootstrap, doc.UnsealDeferredIndexingBootstrap}, {resourceupdate.SealForBootstrap, resourceupdate.UnsealBootstrap},
	}
	for _, o := range owners {
		ticket, err := o.seal(key)
		if err != nil {
			for _, resume := range c.bootstrapResumes {
				_ = resume(key)
			}
			c.bootstrapTickets = nil
			c.bootstrapResumes = nil
			return err
		}
		c.bootstrapTickets = append(c.bootstrapTickets, ticket)
		c.bootstrapResumes = append(c.bootstrapResumes, o.resume)
	}
	c.bootstrapTx, c.bootstrapOwner = tx, owner
	return nil
}
func (c *desktopUpdateControl) CompleteUpdateBootstrap(tx, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bootstrapTx == "" {
		if tx == c.completedTx && owner == c.completedOwner && tx != "" {
			return nil
		}
		return maintenancegate.ErrStale
	}
	if tx != c.bootstrapTx || owner != c.bootstrapOwner {
		return maintenancegate.ErrStale
	}
	for _, t := range c.bootstrapTickets {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	if c.onBootstrapRelease != nil {
		if err := c.onBootstrapRelease(); err != nil {
			return err
		}
	}
	for _, resume := range c.bootstrapResumes {
		if err := resume(tx + "\x00" + owner); err != nil {
			return err
		}
	}
	c.completedTx, c.completedOwner = tx, owner
	c.bootstrapTx, c.bootstrapOwner = "", ""
	c.bootstrapTickets = nil
	c.bootstrapResumes = nil
	return nil
}

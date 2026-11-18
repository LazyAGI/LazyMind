package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type dependencyNoNetwork struct{}

func (dependencyNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected network in waiting dependency fixture")
}

func TestManualDependencyPauseHandoff(t *testing.T) {
	for _, mode := range []string{"pause", "terminal", "normal"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestManualDependencyPauseChild$", "-test.v")
			cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_DEPENDENCY_PAUSE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("dependency child: %v\n%s", err, out)
			}
		})
	}
}

func TestManualDependencyPauseChild(t *testing.T) {
	mode := os.Getenv("LAZYMIND_UNIT_DEPENDENCY_PAUSE")
	if mode == "" {
		return
	}
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	http.DefaultClient = &http.Client{Transport: dependencyNoNetwork{}}
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	now := time.Now().UTC()
	for _, id := range []string{"source", "target"} {
		s := orm.UserSchedule{ID: id, UserID: "owner", Name: id, CronExpr: "0 * * * *", Timezone: "UTC", PromptTemplate: "fixture", Enabled: true}
		if err := CreateSchedule(t.Context(), db.DB, &s); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.ScheduleDependency{ID: "dependency", UserID: "owner", SourceScheduleID: "source", TargetScheduleID: "target", Enabled: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	unrelated := orm.TaskCenterTask{ID: "independent-waiting", UserID: "other-owner", TaskType: "scheduled", Status: "waiting_inputs", DependencyStatus: "waiting", Attempt: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
	if err := db.Create(&unrelated).Error; err != nil {
		t.Fatal(err)
	}
	preparing, releasePrepare := make(chan struct{}), make(chan struct{})
	claiming, releaseClaim := make(chan struct{}), make(chan struct{})
	var prepareOnce, claimOnce, releasePrepareOnce, releaseClaimOnce sync.Once
	unblockPrepare := func() { releasePrepareOnce.Do(func() { close(releasePrepare) }) }
	unblockClaim := func() { releaseClaimOnce.Do(func() { close(releaseClaim) }) }
	if err := db.Callback().Create().Before("gorm:create").Register("unit:hold-manual-waiting", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_center_tasks" {
			prepareOnce.Do(func() { close(preparing) })
			<-releasePrepare
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register("unit:hold-dependency-claim", func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if ok && tx.Statement.Table == "task_center_tasks" && values["dependency_status"] == "checking" {
			claimOnce.Do(func() { close(claiming) })
			<-releaseClaim
		}
	}); err != nil {
		t.Fatal(err)
	}
	reply := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	request := httptest.NewRequest(http.MethodPost, "/schedules/target:run-now", nil)
	request.Header.Set("X-User-Id", "owner")
	go func() { defer close(handlerDone); RunNowHandler(reply, request) }()
	var scanDone chan struct{}
	defer func() {
		unblockPrepare()
		unblockClaim()
		select {
		case <-handlerDone:
		case <-time.After(3 * time.Second):
			t.Error("handler fixture did not return")
		}
		if scanDone != nil {
			select {
			case <-scanDone:
			case <-time.After(3 * time.Second):
				t.Error("scan fixture did not return")
			}
		}
		select {
		case <-BeginDrain():
		case <-time.After(3 * time.Second):
			t.Error("dependency fixture did not drain")
		}
	}()
	select {
	case <-preparing:
	case <-time.After(3 * time.Second):
		t.Fatal("manual dependency preparation not entered")
	}
	var ticket *maintenancegate.Ticket
	var terminal <-chan struct{}
	if mode == "pause" {
		var err error
		ticket, err = PauseForUpdate(1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "terminal" {
		terminal = BeginDrain()
	}
	unblockPrepare()
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("manual handler did not return")
	}
	if reply.Code != http.StatusOK {
		t.Fatalf("admitted manual request rejected: %d", reply.Code)
	}
	var accepted struct {
		ID     string `json:"task_id"`
		Status string `json:"status"`
	}
	if json.Unmarshal(reply.Body.Bytes(), &accepted) != nil || accepted.ID == "" || accepted.Status != "waiting_inputs" {
		t.Fatalf("invalid manual acceptance: %s", reply.Body.String())
	}
	if mode == "terminal" {
		select {
		case <-terminal:
		case <-time.After(3 * time.Second):
			t.Fatal("terminal drain did not preserve queued acceptance")
		}
		select {
		case <-claiming:
			t.Fatal("terminal shutdown started a new dependency claim")
		default:
		}
	} else {
		waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var idle chan error
		if ticket != nil {
			idle = make(chan error, 1)
			go func() { idle <- ticket.Wait(waitCtx) }()
		}
		select {
		case <-claiming:
		case err := <-idle:
			t.Fatalf("pause completed before admitted manual dependency claim: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("admitted continuation did not process dependency")
		}
		if mode == "pause" {
			if ticket.Validate() != maintenancegate.ErrActive {
				t.Fatal("pause did not retain dependency claim")
			}
			scanDone = make(chan struct{})
			go func() { defer close(scanDone); resumeWaitingTasks(context.Background(), db.DB) }()
			select {
			case <-scanDone:
			case <-time.After(time.Second):
				t.Error("independent scan was not rejected during pause")
			}
		} else {
			terminal = BeginDrain()
		}
		unblockClaim()
		if idle != nil {
			select {
			case err := <-idle:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("manual dependency did not drain")
			}
		}
		if terminal != nil {
			select {
			case <-terminal:
			case <-time.After(3 * time.Second):
				t.Fatal("normal admitted scan did not drain")
			}
		}
	}
	var manual orm.TaskCenterTask
	if err := db.First(&manual, "id = ?", accepted.ID).Error; err != nil {
		t.Fatal(err)
	}
	wantAttempt := 2
	wantStatus, wantDependency := "failed", "no_inputs"
	if mode == "terminal" {
		wantAttempt = 1
		wantStatus, wantDependency = "waiting_inputs", "waiting"
	}
	if manual.Status != wantStatus || manual.DependencyStatus != wantDependency || manual.Attempt != wantAttempt {
		t.Fatalf("manual dependency outcome: status=%s dependency=%s attempt=%d", manual.Status, manual.DependencyStatus, manual.Attempt)
	}
	if err := db.First(&unrelated, "id = ?", unrelated.ID).Error; err != nil {
		t.Fatal(err)
	}
	if mode == "normal" {
		if unrelated.Status != "failed" || unrelated.Attempt != 2 {
			t.Fatal("ordinary opportunity scan changed behavior")
		}
	} else if unrelated.Status != "waiting_inputs" || unrelated.Attempt != 1 {
		t.Fatal("pause continuation processed an unrelated new root")
	}
}

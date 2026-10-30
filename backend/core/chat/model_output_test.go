package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestModelOutputReplacementJSONKeepsExplicitEmptyProjection(t *testing.T) {
	var chunk ChatChunkResponse
	if err := json.Unmarshal([]byte(`{"delta_mode":"replace","delta":"","reasoning_content":"","sources":[],"exports":[],"model_output":{"run_id":"run","call_index":2,"phase":"rollback"}}`), &chunk); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, field := range []string{"model_output", "reasoning_content", "sources", "exports"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("absolute projection omitted %s: %s", field, raw)
		}
	}
	if string(fields["sources"]) != "[]" || string(fields["reasoning_content"]) != `""` {
		t.Fatalf("empty projection is not explicit: %s", raw)
	}
}

func TestModelOutputSnapshotsStayWithinLatestSteeringSegment(t *testing.T) {
	const runID = "steered-output"
	prior := "<think>first reasoning</think>first reply"
	middle := "<think>second reasoning</think>second reply"
	tail := "<think>latest reasoning</think>latest reply"
	ext := withSteeringBoundary(nil, runID, len(prior), []steeringApplied{{ID: "first", Text: "first guide"}})
	ext = withSteeringBoundary(ext, runID, len(prior+middle), []steeringApplied{{ID: "second", Text: "second guide"}})
	owner := newChatModelOutput(runID)
	owner.cursor.CallIndex = 3
	ext = mergeModelOutputIntoExt(ext, owner.metadata())
	history := orm.ChatHistory{ID: "history", RunID: runID, Result: prior + middle + tail, Ext: ext}
	projection := chatOutputProjection{text: "first replysecond replylatest reply", result: history.Result}
	replaced := owner.replacement(projection, ext, "conversation", history.ID, 1)
	if replaced.Delta != "latest reply" || replaced.ReasoningContent != "latest reasoning" {
		t.Fatalf("replacement crossed steering boundary: %+v", replaced)
	}
	fields := chatHistoryToResponseItem(history)
	if fields["reasoning_content"] != "latest reasoning" || fields["assistant_remainder"] != "latest reply" {
		t.Fatalf("cold snapshot crossed steering boundary: %+v", fields)
	}
	chunks := []*ChatChunkResponse{
		{Delta: "first reply", ReasoningContent: "first reasoning"},
		{SteeringApplied: []steeringApplied{{ID: "first"}}},
		{Delta: "second reply", ReasoningContent: "second reasoning"},
		{SteeringApplied: []steeringApplied{{ID: "second"}}},
		{Delta: "failed", ReasoningContent: "failed reasoning"}, replaced,
	}
	replay := steeringResumeChunk(chunks, mergeChunksToFirstChunk(chunks))
	if replay.Delta != "latest reply" || replay.ReasoningContent != "latest reasoning" || replay.SteeringReplay[1].AssistantSegment != "second reply" {
		t.Fatalf("cache snapshot crossed steering boundary: %+v", replay)
	}
}

func TestModelOutputOpenProgressKeepsCommittedReferencesAndArtifact(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "false")
	db, conversation, body := fallbackTestBody(t, "fixed")
	release := make(chan struct{})
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		encoder := json.NewEncoder(w)
		emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
		model := func(index int, phase string, data map[string]any) {
			metadata := map[string]any{"run_id": request.Conversation.RunID, "call_index": index}
			if phase != "" {
				metadata["phase"] = phase
			}
			data["model_output"] = metadata
			emit(data)
		}
		model(1, "begin", map[string]any{})
		model(1, "", map[string]any{"think": "kept-think"})
		model(1, "", map[string]any{"text": "kept", "sources": []any{map[string]any{"id": "kept-reference"}}})
		model(1, "commit", map[string]any{})
		emit(map[string]any{"artifact_created": map[string]any{"artifact_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "filename": "kept.txt", "content_type": "text", "value": map[string]any{"text": "real artifact"}}})
		model(2, "begin", map[string]any{})
		model(2, "", map[string]any{"text": "BAD", "sources": []any{map[string]any{"id": "BAD-reference"}}})
		model(2, "", map[string]any{"think": "BAD-think"})
		w.(http.Flusher).Flush()
		close(ready)
		<-release
		model(2, "rollback", map[string]any{})
		emit(map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "failed", Reason: "model_failure", Code: "model_fallback_exhausted", PartialOutput: true})})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	stateStore := newRunDecisionTestStore(t)
	if err := setChatRuntimeStatus(t.Context(), stateStore, conversation.ID, "progress-output", "generating", "", "run-fallback", nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, stateStore, server.URL, body, conversation.ID, "hello", "progress-output", chatPersistTarget{HistoryID: "progress-output", Seq: 1}, mergeChatModelRouteIntoExt(nil, body))
	}()
	defer func() { close(release); <-done }()
	<-ready
	var history orm.ChatHistory
	deadline := time.Now().Add(5 * time.Second)
	for {
		chunks, _ := getChatChunks(t.Context(), stateStore, conversation.ID, "progress-output")
		seenTemporaryThink := false
		for _, chunk := range chunks {
			seenTemporaryThink = seenTemporaryThink || chunk.ReasoningContent == "\nBAD-think"
		}
		if seenTemporaryThink {
			if err := db.Where("id = ?", "progress-output").Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("temporary preview was not published")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if history.Result != "<think>kept-think</think>kept" || len(retrievalSources(history.RetrievalResult)) != 1 || retrievalSources(history.RetrievalResult)[0].(map[string]any)["id"] != "kept-reference" {
		t.Fatalf("temporary output entered DB progress: result=%q retrieval=%s", history.Result, history.RetrievalResult)
	}
	release <- struct{}{}
	<-done
	if err := db.Where("id = ?", history.ID).Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	merged := mergeChunksToFirstChunk(func() []*ChatChunkResponse {
		chunks := decodeChatChunkSSE(t, recorder.Body.String())
		values := make([]*ChatChunkResponse, len(chunks))
		for i := range chunks {
			values[i] = &chunks[i]
		}
		return values
	}())
	if merged.Delta != "kept" || merged.ReasoningContent != "kept-think" || len(merged.Sources) != 1 || merged.Sources[0].(map[string]any)["id"] != "kept-reference" {
		t.Fatalf("rollback cleared committed references: %+v", merged)
	}
	cold := httptest.NewRecorder()
	resumeFromDBOnly(t.Context(), db, "user-1", conversation.ID, cold, cold)
	frame := decodeChatChunkSSE(t, cold.Body.String())[0]
	if frame.Delta != merged.Delta || frame.ReasoningContent != merged.ReasoningContent || len(frame.Sources) != 1 || frame.ModelOutput.Phase != "rollback" {
		t.Fatalf("cold committed projection differs: %+v", frame)
	}
	var artifact orm.ConversationArtifact
	if err := db.Where("id = ? AND conversation_id = ?", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", conversation.ID).Take(&artifact).Error; err != nil || !strings.Contains(string(artifact.Value), "real artifact") {
		t.Fatalf("rollback removed a real artifact: %v", err)
	}
}

func TestModelOutputRollbackPersistsOnlyCommittedProjection(t *testing.T) {
	for _, stream := range []bool{true, false} {
		name := "nonstream"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			db, conversation, body := fallbackTestBody(t, "auto")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				_ = json.NewDecoder(r.Body).Decode(&request)
				encoder := json.NewEncoder(w)
				emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
				model := func(index int, phase string, data map[string]any) {
					meta := map[string]any{"run_id": request.Conversation.RunID, "call_index": index}
					if phase != "" {
						meta["phase"] = phase
					}
					data["model_output"] = meta
					emit(data)
				}
				model(1, "begin", map[string]any{})
				model(1, "", map[string]any{"think": "prior-think"})
				model(1, "", map[string]any{"text": "kept \n"})
				model(1, "commit", map[string]any{})
				emit(map[string]any{"text": "<tp>tool</tp><trp>done</trp>\n", "tool_call_turns": 1})
				model(2, "begin", map[string]any{})
				model(2, "", map[string]any{"think": "BAD_THINK"})
				model(2, "", map[string]any{"text": "BAD_TEXT", "sources": []any{map[string]any{"id": "bad-source"}}, "export_snapshot": map[string]any{"content": "BAD_EXPORT", "exports": []any{}}})
				model(2, "rollback", map[string]any{})
				model(2, "rollback", map[string]any{})
				model(2, "", map[string]any{"text": "BAD_LATE"})
				model(3, "begin", map[string]any{})
				model(3, "", map[string]any{"text": "good"})
				model(3, "commit", map[string]any{"runtime_event": fallbackSwitchEvent(body, request.Conversation.RunID)})
				emit(map[string]any{"runtime_event": completedRunEvent(request.Conversation.RunID, true)})
			}))
			defer server.Close()
			target := chatPersistTarget{HistoryID: "model-output-history", Seq: 1}
			recorder := httptest.NewRecorder()
			if stream {
				streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
			} else {
				handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, body, conversation.ID, "hello", target, mergeChatModelRouteIntoExt(nil, body))
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			if history.RunStatus != "completed" || strings.Contains(history.Result, "BAD") || !strings.Contains(history.Result, "kept \n") || !strings.Contains(history.Result, "<trp>done</trp>\ngood") || extractThinkContent(history.Result) != "prior-think" || len(retrievalSources(history.RetrievalResult)) != 0 || len(chatExportsFromExt(history.Ext)) != 0 {
				t.Fatalf("failed call survived or committed content disappeared: status=%s result=%q retrieval=%s ext=%s", history.RunStatus, history.Result, history.RetrievalResult, history.Ext)
			}
			cursor := modelOutputFromExt(history.Ext)
			if cursor == nil || cursor.CallIndex != 3 || cursor.Phase != "commit" || cursor.RunID != history.RunID {
				t.Fatalf("wrong durable cursor: %#v", cursor)
			}
			if stream {
				chunks := decodeChatChunkSSE(t, recorder.Body.String())
				cachedChunks := make([]*ChatChunkResponse, len(chunks))
				for i := range chunks {
					cachedChunks[i] = &chunks[i]
				}
				merged := mergeChunksToFirstChunk(cachedChunks)
				if strings.Contains(merged.Delta, "BAD") || merged.ReasoningContent != "prior-think" || merged.ModelOutput == nil || merged.ModelOutput.CallIndex != 3 || len(merged.Sources) != 0 || !strings.HasSuffix(merged.Delta, "</trp>\ngood") {
					t.Fatalf("cache merge differs from committed output: %+v", merged)
				}
				stateStore := newRunDecisionTestStore(t)
				for _, chunk := range chunks {
					if err := appendChatChunk(t.Context(), stateStore, conversation.ID, history.ID, &chunk); err != nil {
						t.Fatal(err)
					}
				}
				terminal, _ := parseRunTerminal(history.RunTerminal)
				if err := setChatRuntimeStatus(t.Context(), stateStore, conversation.ID, history.ID, terminal.Status, merged.Delta, history.RunID, terminal); err != nil {
					t.Fatal(err)
				}
				resume := httptest.NewRecorder()
				resumeSingleAnswerChat(t.Context(), stateStore, conversation.ID, history.ID, resume, resume)
				cached := decodeChatChunkSSE(t, resume.Body.String())[0]
				if cached.Delta != merged.Delta || cached.ReasoningContent != merged.ReasoningContent || cached.ModelOutput.CallIndex != 3 {
					t.Fatalf("cache resume changed projection: %+v", cached)
				}
			}
			cold := httptest.NewRecorder()
			resumeFromDBOnly(t.Context(), db, "user-1", conversation.ID, cold, cold)
			restored := decodeChatChunkSSE(t, cold.Body.String())[0]
			if strings.Contains(restored.Delta, "BAD") || restored.ReasoningContent != "prior-think" || restored.ModelOutput == nil || restored.ModelOutput.CallIndex != 3 || len(restored.Sources) != 0 || restored.Exports == nil || len(*restored.Exports) != 0 {
				t.Fatalf("cold resume lost absolute projection: %+v", restored)
			}
		})
	}
}

func TestModelOutputOpenCallClosesBeforeTerminal(t *testing.T) {
	for _, stream := range []bool{true, false} {
		for _, prior := range []bool{true, false} {
			for _, outcome := range []string{"length", "cancel", "disconnect", "non200"} {
				t.Run(fmt.Sprintf("stream=%t/prior=%t/%s", stream, prior, outcome), func(t *testing.T) {
					db, conversation, body := fallbackTestBody(t, "fixed")
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var request LazyChatRequest
						_ = json.NewDecoder(r.Body).Decode(&request)
						encoder := json.NewEncoder(w)
						model := func(index int, phase, text string) {
							metadata := map[string]any{"run_id": request.Conversation.RunID, "call_index": index}
							if phase != "" {
								metadata["phase"] = phase
							}
							_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"text": text, "model_output": metadata}})
						}
						index := 1
						if prior {
							model(1, "begin", "")
							model(1, "", "kept")
							model(1, "commit", "")
							index = 2
						}
						model(index, "begin", "")
						model(index, "", "BAD")
						if outcome == "non200" {
							_ = encoder.Encode(map[string]any{"code": 500, "msg": "fixture service failure"})
							return
						}
						if outcome == "disconnect" {
							return
						}
						terminal := RunTerminal{Status: "interrupted", Reason: "model_incomplete", Code: "length"}
						if outcome == "cancel" {
							terminal = RunTerminal{Status: "cancelled", Reason: "user_cancelled"}
						}
						_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"model_output": map[string]any{"run_id": request.Conversation.RunID, "call_index": index, "phase": "rollback"}, "runtime_event": runFinishedEvent(request.Conversation.RunID, terminal), "tool_call_turns": 3}})
					}))
					defer server.Close()
					recorder := httptest.NewRecorder()
					target := chatPersistTarget{HistoryID: "open-output", Seq: 1}
					if stream {
						streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
					} else {
						handleNonStreamChat(recorder, t.Context(), db, nil, server.URL, body, conversation.ID, "hello", target, mergeChatModelRouteIntoExt(nil, body))
					}
					var history orm.ChatHistory
					if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
						t.Fatal(err)
					}
					want := ""
					if prior {
						want = "kept"
					}
					terminal, err := parseRunTerminal(history.RunTerminal)
					if err != nil || history.Result != want || terminal.PartialOutput != prior {
						t.Fatalf("open output survived terminal: result=%q terminal=%s err=%v", history.Result, history.RunTerminal, err)
					}
					if (outcome == "length" || outcome == "cancel") && history.ToolCallTurns != 3 {
						t.Fatalf("terminal observation lost tool count: %d", history.ToolCallTurns)
					}
					if outcome == "length" && (terminal.Status != "interrupted" || terminal.Code != "length") || outcome == "cancel" && terminal.Status != "cancelled" || (outcome == "disconnect" || outcome == "non200") && terminal.Status != "failed" {
						t.Fatalf("terminal was replaced: %s", history.RunTerminal)
					}
					if outcome == "non200" && terminal.Code != "upstream_stream_failed" {
						t.Fatalf("service failure lost its stable code: %s", history.RunTerminal)
					}
					cursor := modelOutputFromExt(history.Ext)
					if cursor == nil || cursor.Phase != "rollback" {
						t.Fatal("open cursor did not close")
					}
					if !stream && recorder.Code != http.StatusOK {
						t.Fatalf("authoritative fenced terminal became HTTP %d", recorder.Code)
					}
					if stream {
						chunks := decodeChatChunkSSE(t, recorder.Body.String())
						replaced, terminals := false, 0
						for _, chunk := range chunks {
							if chunk.ModelOutput != nil && chunk.ModelOutput.Phase == "rollback" && chunk.DeltaMode == ChatDeltaModeReplace {
								replaced = true
								if chunk.Delta != want || chunk.ReasoningContent != "" || len(chunk.Sources) != 0 || chunk.Exports == nil {
									t.Fatalf("bad rollback: %+v", chunk)
								}
							}
							if chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
								if !replaced {
									t.Fatal("terminal preceded rollback")
								}
								terminals++
							}
						}
						if terminals != 1 {
							t.Fatalf("terminal count=%d", terminals)
						}
					}
				})
			}
		}
	}
}

func TestModelOutputFenceRejectsMalformedAndDropsClosedCalls(t *testing.T) {
	owner := newChatModelOutput("run")
	projection := chatOutputProjection{text: "kept"}
	begin := UpstreamStreamChunk{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 1, Phase: "begin"}}
	if accepted, err := owner.accept(begin); err != nil || !accepted {
		t.Fatal(err)
	}
	owner.apply(begin.ModelOutput, &projection)
	projection.text += "BAD"
	rollback := UpstreamStreamChunk{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 1, Phase: "rollback"}}
	owner.apply(rollback.ModelOutput, &projection)
	for _, phase := range []string{"begin", "", "commit", "rollback"} {
		chunk := UpstreamStreamChunk{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 1, Phase: phase}}
		if accepted, err := owner.accept(chunk); accepted || err != nil {
			t.Fatalf("closed %q was accepted=%t err=%v", phase, accepted, err)
		}
	}
	if projection.text != "kept" {
		t.Fatal("closed call changed projection")
	}
	if accepted, err := owner.accept(UpstreamStreamChunk{ModelOutput: &ChatModelOutput{RunID: "old-run", CallIndex: 2, Phase: "begin"}}); accepted || err != nil {
		t.Fatal("old run reopened output")
	}
	for _, chunk := range []UpstreamStreamChunk{
		{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 0, Phase: "begin"}},
		{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 2, Phase: "invalid"}},
		{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 2, Phase: "begin"}, Text: "mixed"},
		{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 2, Phase: "commit"}, ModelContextUpdated: &ModelContextUpdatedEvent{SummaryText: "sidecar"}},
		{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 3, Phase: "begin"}},
	} {
		if _, err := owner.accept(chunk); err == nil {
			t.Fatalf("invalid boundary was accepted: %+v", chunk)
		}
	}
	next := UpstreamStreamChunk{ModelOutput: &ChatModelOutput{RunID: "run", CallIndex: 2, Phase: "begin"}}
	if accepted, err := owner.accept(next); !accepted || err != nil {
		t.Fatal("closed call prevented the next call")
	}
}

func TestModelOutputInvalidSwitchCannotCommitProjection(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "auto")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		e := json.NewEncoder(w)
		metadata := func(phase string) map[string]any {
			return map[string]any{"run_id": request.Conversation.RunID, "call_index": 1, "phase": phase}
		}
		_ = e.Encode(map[string]any{"code": 200, "data": map[string]any{"model_output": metadata("begin")}})
		_ = e.Encode(map[string]any{"code": 200, "data": map[string]any{"model_output": map[string]any{"run_id": request.Conversation.RunID, "call_index": 1}, "text": "BAD"}})
		state := body[chatModelFallbackBodyKey].(*chatFallbackState)
		switched := fallbackWireEvent(request.Conversation.RunID, "forged-switch", RuntimeEventModelSwitched, ModelSwitchedData{SwitchIndex: 1, From: state.identity("own:model-a"), To: ModelSwitchIdentity{CandidateID: "own:private", Source: "own", ModelID: "private"}, ReasonCode: "not_found"})
		_ = e.Encode(map[string]any{"code": 200, "data": map[string]any{"model_output": metadata("commit"), "runtime_event": switched}})
		_ = e.Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(request.Conversation.RunID, true)}})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	target := chatPersistTarget{HistoryID: "invalid-output-switch", Seq: 1}
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
	var history orm.ChatHistory
	if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	if history.Result != "" || history.RunStatus != "failed" || chatModelRouteFromBody(body).ModelID != "model-a" || len(modelSwitchesFromExt(history.Ext)) != 0 || modelOutputFromExt(history.Ext).Phase != "rollback" {
		t.Fatalf("invalid commit split output and route: result=%q ext=%s", history.Result, history.Ext)
	}
}

func TestModelOutputCacheResumeBindsLatestRun(t *testing.T) {
	store := newRunDecisionTestStore(t)
	chunks := []*ChatChunkResponse{
		{ModelOutput: &ChatModelOutput{RunID: "old", CallIndex: 0, Phase: "commit"}},
		{Delta: "old content"},
		{ModelOutput: &ChatModelOutput{RunID: "new", CallIndex: 0, Phase: "commit"}},
		{ModelOutput: &ChatModelOutput{RunID: "new", CallIndex: 1, Phase: "begin"}},
		{Delta: "new content", ModelOutput: &ChatModelOutput{RunID: "new", CallIndex: 1}},
		{ModelOutput: &ChatModelOutput{RunID: "new", CallIndex: 1, Phase: "commit"}},
		{Delta: "BAD old callback", ModelOutput: &ChatModelOutput{RunID: "old", CallIndex: 2}},
		{RuntimeEvent: completedRunEvent("old", true)},
	}
	for _, chunk := range chunks {
		if err := appendChatChunk(t.Context(), store, "conv", "history", chunk); err != nil {
			t.Fatal(err)
		}
	}
	terminal := &RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true}
	if err := setChatRuntimeStatus(t.Context(), store, "conv", "history", "completed", "new content", "new", terminal); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	resumeSingleAnswerChat(t.Context(), store, "conv", "history", recorder, recorder)
	frames := decodeChatChunkSSE(t, recorder.Body.String())
	if len(frames) != 2 || frames[0].Delta != "new content" || frames[0].ModelOutput.RunID != "new" || frames[1].RuntimeEvent.RunID != "new" {
		t.Fatalf("old run escaped cache resume: %+v", frames)
	}
}

func TestModelOutputCacheTerminalRestoresOpenProjection(t *testing.T) {
	for _, recovery := range []string{"cached-terminal", "status-only", "active-status"} {
		t.Run(recovery, func(t *testing.T) {
			store := newRunDecisionTestStore(t)
			metadata := func(index int, phase string) *ChatModelOutput {
				return &ChatModelOutput{RunID: "run", CallIndex: index, Phase: phase}
			}
			chunks := []*ChatChunkResponse{
				{ModelOutput: metadata(0, "commit")},
				{ModelOutput: metadata(1, "begin")},
				{Delta: "kept\n", ReasoningContent: "kept-think", Sources: []any{map[string]any{"id": "kept-ref"}}, ModelOutput: metadata(1, "")},
				{ModelOutput: metadata(1, "commit")},
				{Delta: "<tp>real tool</tp><trp>done</trp>\n"},
				{ModelOutput: metadata(2, "begin")},
				{Delta: "BAD", ReasoningContent: "BAD-think", Sources: []any{map[string]any{"id": "bad-ref"}}, ModelOutput: metadata(2, "")},
			}
			terminal := &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "upstream_stream_failed", PartialOutput: true}
			if recovery == "cached-terminal" {
				chunks = append(chunks, &ChatChunkResponse{RuntimeEvent: runFinishedEvent("run", *terminal)})
			}
			for _, chunk := range chunks {
				if err := appendChatChunk(t.Context(), store, "conv", "history", chunk); err != nil {
					t.Fatal(err)
				}
			}
			var frames []ChatChunkResponse
			if recovery == "active-status" {
				if err := setChatRuntimeStatus(t.Context(), store, "conv", "history", "generating", "", "run", nil); err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					resumeSingleAnswerChat(r.Context(), store, "conv", "history", w, w.(http.Flusher))
				}))
				defer server.Close()
				response, err := server.Client().Get(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				reader := bufio.NewReader(response.Body)
				first, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if err := setChatRuntimeStatus(t.Context(), store, "conv", "history", "failed", "kept\n<trp>done</trp>\n", "run", terminal); err != nil {
					t.Fatal(err)
				}
				rest, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				frames = decodeChatChunkSSE(t, first+string(rest))
				if len(frames) != 3 || !strings.Contains(frames[0].Delta, "BAD") {
					t.Fatalf("active preview was not sent first: %+v", frames)
				}
				frames = frames[1:]
			} else {
				if err := setChatRuntimeStatus(t.Context(), store, "conv", "history", "failed", "kept\n<trp>done</trp>\n", "run", terminal); err != nil {
					t.Fatal(err)
				}
				recorder := httptest.NewRecorder()
				resumeSingleAnswerChat(t.Context(), store, "conv", "history", recorder, recorder)
				frames = decodeChatChunkSSE(t, recorder.Body.String())
			}
			if len(frames) != 2 || strings.Contains(frames[0].Delta, "BAD") || frames[0].Delta != "kept\n<tp>real tool</tp><trp>done</trp>\n" || frames[0].ReasoningContent != "kept-think" || len(frames[0].Sources) != 1 || frames[0].Sources[0].(map[string]any)["id"] != "kept-ref" || frames[0].ModelOutput.Phase != "rollback" {
				t.Fatalf("open cache preview survived terminal: %+v", frames)
			}
		})
	}
}

func TestModelOutputClosedFinishExportsRemainDeliverable(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	snapshot := ChatExportSnapshot{Content: "report body", Exports: []ChatExport{{Title: "report", Filename: "report.md", ContentType: "text/markdown", Start: 0, End: 11}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		encoder := json.NewEncoder(w)
		emit := func(data map[string]any) { _ = encoder.Encode(map[string]any{"code": 200, "data": data}) }
		model := func(index int, phase string, data map[string]any) {
			metadata := map[string]any{"run_id": request.Conversation.RunID, "call_index": index}
			if phase != "" {
				metadata["phase"] = phase
			}
			data["model_output"] = metadata
			emit(data)
		}
		model(1, "begin", map[string]any{})
		model(1, "", map[string]any{"think": "kept-think"})
		model(1, "", map[string]any{"text": "commentary\n"})
		model(1, "commit", map[string]any{})
		emit(map[string]any{"text": "<tp>tool</tp><trp>done</trp>\n"})
		model(2, "begin", map[string]any{})
		model(2, "", map[string]any{"text": "report body\n"})
		model(2, "commit", map[string]any{})
		emit(map[string]any{"export_snapshot": snapshot, "runtime_event": completedRunEvent(request.Conversation.RunID, true)})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	target := chatPersistTarget{HistoryID: "closed-finish-export", Seq: 1}
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	var final *ChatChunkResponse
	values := make([]*ChatChunkResponse, len(chunks))
	for i := range chunks {
		values[i] = &chunks[i]
		if chunks[i].Exports != nil {
			final = &chunks[i]
		}
	}
	expectedRaw := "commentary\n<tp>tool</tp><trp>done</trp>report body"
	if final == nil || final.DeltaMode != ChatDeltaModeReplace || final.Delta != expectedRaw || len(*final.Exports) != 1 {
		t.Fatalf("missing final export replacement: %+v", final)
	}
	if final.ModelOutput != nil {
		t.Errorf("closed finish snapshot would be dropped by the closed-call fence: %+v", final.ModelOutput)
	}
	merged := mergeChunksToFirstChunk(values)
	if merged.Delta != expectedRaw || merged.ReasoningContent != "kept-think" || merged.Exports == nil || len(*merged.Exports) != 1 || merged.ModelOutput == nil || merged.ModelOutput.CallIndex != 2 || merged.ModelOutput.Phase != "commit" {
		t.Errorf("cache did not retain the deliverable finish and closed cursor: %+v", merged)
	}
	var history orm.ChatHistory
	if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history.Result, "<trp>done</trp>") || extractThinkContent(history.Result) != "kept-think" || !strings.HasSuffix(history.Result, snapshot.Content) || len(chatExportsFromExt(history.Ext)) != 1 {
		t.Fatalf("finish removed committed tool/thinking or exports: result=%q ext=%s", history.Result, history.Ext)
	}
	cold := httptest.NewRecorder()
	resumeFromDBOnly(t.Context(), db, "user-1", conversation.ID, cold, cold)
	restored := decodeChatChunkSSE(t, cold.Body.String())[0]
	if restored.Delta != expectedRaw || restored.ModelOutput == nil || restored.ModelOutput.CallIndex != 2 || restored.ReasoningContent != "kept-think" || restored.Exports == nil || len(*restored.Exports) != 1 || (*restored.Exports)[0].ExportID != (*final.Exports)[0].ExportID {
		t.Fatalf("cold finish differs from committed metadata: %+v", restored)
	}
}

func TestModelOutputCanonicalThinkingDeltasMatchHistoryAndResume(t *testing.T) {
	db, conversation, body := fallbackTestBody(t, "fixed")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		encoder := json.NewEncoder(w)
		model := func(phase, field, value string) {
			metadata := map[string]any{"run_id": request.Conversation.RunID, "call_index": 1}
			if phase != "" {
				metadata["phase"] = phase
			}
			data := map[string]any{"model_output": metadata}
			if field != "" {
				data[field] = value
			}
			_ = encoder.Encode(map[string]any{"code": 200, "data": data})
		}
		model("begin", "", "")
		model("", "think", " \tFIRST  ")
		model("", "think", "MORE   ")
		model("", "text", "body\n")
		model("", "think", " \t ")
		model("", "think", " SECOND  ")
		model("", "think", "TAIL  ")
		model("commit", "", "")
		_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(request.Conversation.RunID, true)}})
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	target := chatPersistTarget{HistoryID: "canonical-thinking", Seq: 1}
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, mergeChatModelRouteIntoExt(nil, body))
	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	values := make([]*ChatChunkResponse, len(chunks))
	var sdkThinking string
	secondBlock := -1
	for i := range chunks {
		values[i] = &chunks[i]
		sdkThinking += chunks[i].ReasoningContent
		if secondBlock < 0 && strings.Contains(chunks[i].ReasoningContent, "SECOND") {
			secondBlock = i
		}
	}
	expected := "FIRST  MORE\nSECOND  TAIL"
	if sdkThinking != expected {
		t.Errorf("sender SDK delta blocks differ from history semantics: %q", sdkThinking)
	}
	merged := mergeChunksToFirstChunk(values)
	if merged.ReasoningContent != expected {
		t.Errorf("cache SDK thinking differs: %q", merged.ReasoningContent)
	}
	if secondBlock < 0 {
		t.Fatal("missing second thinking block")
	}
	checkpoint := mergeChunksToFirstChunk(values[:secondBlock])
	resumed := checkpoint.ReasoningContent
	for _, chunk := range chunks[secondBlock:] {
		resumed += chunk.ReasoningContent
	}
	if checkpoint.ModelOutput == nil || checkpoint.ModelOutput.Phase != "begin" || checkpoint.ModelOutput.CallIndex != 1 || resumed != expected {
		t.Errorf("same-call continuation after an open resume snapshot differs: checkpoint=%+v final=%q", checkpoint, resumed)
	}
	var history orm.ChatHistory
	if err := db.Where("id = ?", target.HistoryID).Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	if extractThinkContent(history.Result) != expected {
		t.Fatalf("invalid history oracle: %q", history.Result)
	}
	cold := httptest.NewRecorder()
	resumeFromDBOnly(t.Context(), db, "user-1", conversation.ID, cold, cold)
	if restored := decodeChatChunkSSE(t, cold.Body.String())[0]; restored.ReasoningContent != expected {
		t.Fatalf("cold thinking differs: %+v", restored)
	}
}

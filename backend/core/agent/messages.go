package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
)

type activeMessageStream struct {
	threadID    string
	roundID     string
	requestHash string
	done        chan struct{}
	subscribers map[*messageStreamSubscription]struct{}

	mu  sync.RWMutex
	err error
}

type messageStreamSubscription struct {
	records    chan orm.AgentThreadRecord
	heartbeats chan struct{}
	done       chan struct{}
	once       sync.Once
}

func newMessageStreamSubscription() *messageStreamSubscription {
	return &messageStreamSubscription{
		records:    make(chan orm.AgentThreadRecord, 256),
		heartbeats: make(chan struct{}, 16),
		done:       make(chan struct{}),
	}
}

func (s *messageStreamSubscription) close() {
	s.once.Do(func() {
		close(s.done)
	})
}

func (s *activeMessageStream) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *activeMessageStream) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func (s *activeMessageStream) subscribe() *messageStreamSubscription {
	sub := newMessageStreamSubscription()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscribers == nil {
		s.subscribers = make(map[*messageStreamSubscription]struct{})
	}
	s.subscribers[sub] = struct{}{}
	return sub
}

func (s *activeMessageStream) unsubscribe(sub *messageStreamSubscription) {
	if sub == nil {
		return
	}
	s.mu.Lock()
	delete(s.subscribers, sub)
	s.mu.Unlock()
	sub.close()
}

func (s *activeMessageStream) publish(record orm.AgentThreadRecord) {
	s.mu.RLock()
	subscribers := make([]*messageStreamSubscription, 0, len(s.subscribers))
	for sub := range s.subscribers {
		subscribers = append(subscribers, sub)
	}
	s.mu.RUnlock()

	for _, sub := range subscribers {
		select {
		case sub.records <- record:
		case <-sub.done:
		case <-s.done:
			return
		}
	}
}

func (s *activeMessageStream) publishHeartbeat() {
	s.mu.RLock()
	subscribers := make([]*messageStreamSubscription, 0, len(s.subscribers))
	for sub := range s.subscribers {
		subscribers = append(subscribers, sub)
	}
	s.mu.RUnlock()

	for _, sub := range subscribers {
		select {
		case sub.heartbeats <- struct{}{}:
		case <-sub.done:
		case <-s.done:
		default:
		}
	}
}

func (s *activeMessageStream) closeSubscribers() {
	s.mu.Lock()
	subscribers := make([]*messageStreamSubscription, 0, len(s.subscribers))
	for sub := range s.subscribers {
		subscribers = append(subscribers, sub)
		delete(s.subscribers, sub)
	}
	s.mu.Unlock()

	for _, sub := range subscribers {
		sub.close()
	}
}

type messageStreamRegistry struct {
	mu       sync.Mutex
	sessions map[string]*activeMessageStream
}

var activeStreams = &messageStreamRegistry{
	sessions: make(map[string]*activeMessageStream),
}

func (r *messageStreamRegistry) get(threadID string) *activeMessageStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[threadID]
	if session == nil {
		return nil
	}
	select {
	case <-session.done:
		delete(r.sessions, threadID)
		return nil
	default:
		return session
	}
}

func (r *messageStreamRegistry) put(threadID string, session *activeMessageStream) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.sessions[threadID]; current != nil {
		select {
		case <-current.done:
			delete(r.sessions, threadID)
		default:
			return false
		}
	}
	r.sessions[threadID] = session
	return true
}

func (r *messageStreamRegistry) delete(threadID string, session *activeMessageStream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.sessions[threadID]; current == session {
		delete(r.sessions, threadID)
	}
}

func ensureMessageStream(
	db *gorm.DB,
	thread orm.AgentThread,
	requestBody []byte,
	headers map[string]string,
) (*activeMessageStream, error) {
	persistedRequestBody := stripLLMConfigFromRequestBody(requestBody)
	requestHash := sha256Hex(string(persistedRequestBody))
	if existing := activeStreams.get(thread.ThreadID); existing != nil {
		if existing.requestHash != requestHash {
			return nil, fmt.Errorf("thread already has an active messages stream")
		}
		return existing, nil
	}

	resp, err := openMessageStream(thread.ThreadID, requestBody, headers)
	if err != nil {
		return nil, err
	}

	round, err := createThreadRound(db, thread.ThreadID, requestHash, persistedRequestBody)
	if err != nil {
		_ = resp.Body.Close()
		return nil, err
	}

	session := &activeMessageStream{
		threadID:    thread.ThreadID,
		roundID:     round.RoundID,
		requestHash: requestHash,
		done:        make(chan struct{}),
		subscribers: make(map[*messageStreamSubscription]struct{}),
	}
	if !activeStreams.put(thread.ThreadID, session) {
		_ = resp.Body.Close()
		if existing := activeStreams.get(thread.ThreadID); existing != nil {
			if existing.requestHash != requestHash {
				return nil, fmt.Errorf("thread already has an active messages stream")
			}
			return existing, nil
		}
		return ensureMessageStream(db, thread, requestBody, headers)
	}

	now := time.Now().UTC()
	_ = db.Model(&orm.AgentThread{}).
		Where("thread_id = ?", thread.ThreadID).
		Updates(map[string]any{
			"status":                    "message_streaming",
			"last_message_request_hash": requestHash,
			"updated_at":                now,
		}).Error

	go consumeMessageStream(db, session, thread.ThreadID, resp)
	return session, nil
}

func openMessageStream(threadID string, requestBody []byte, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, threadMessagesURL(threadID), bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		if strings.EqualFold(key, "Accept") {
			continue
		}
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var payload any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
			return nil, fmt.Errorf("upstream messages request failed: %v", payload)
		}
		return nil, fmt.Errorf("upstream messages request failed with status %d", resp.StatusCode)
	}
	return resp, nil
}

func stripLLMConfigFromRequestBody(requestBody []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(requestBody, &payload); err != nil {
		return requestBody
	}
	delete(payload, "llm_config")
	out, err := json.Marshal(payload)
	if err != nil {
		return requestBody
	}
	return out
}

func consumeMessageStream(db *gorm.DB, session *activeMessageStream, threadID string, resp *http.Response) {
	defer func() {
		_ = resp.Body.Close()
		activeStreams.delete(threadID, session)
		session.closeSubscribers()
		close(session.done)
	}()

	reader := bufio.NewReader(resp.Body)
	status := "completed"
	var assistantMessage strings.Builder
	for {
		frame, err := readSSEFrame(reader)
		if err != nil {
			if err != io.EOF {
				status = "failed"
				session.setErr(err)
				log.Logger.Error().Err(err).Str("thread_id", threadID).Msg("consume upstream message stream failed")
			}
			break
		}

		rawData := strings.TrimSpace(frame.Data)
		payload := parseJSONValue(rawData)
		if shouldSkipStreamData(frame.Event, payload, rawData) {
			if rawData == "[DONE]" {
				break
			}
			session.publishHeartbeat()
			log.Logger.Info().
				Str("thread_id", threadID).
				Str("round_id", session.roundID).
				Str("sse_endpoint", ":messages").
				Str("event_name", strings.TrimSpace(frame.Event)).
				Int("data_bytes", len(rawData)).
				Msg("agent messages upstream frame skipped; keepalive published")
			continue
		}

		taskID := ""
		if payload != nil {
			taskID = extractStringByKeys(payload, "task_id", "current_task_id")
		}
		logUpstreamSSEData(":messages", threadID, session.roundID, taskID, frame.Event, rawData)
		if delta := extractAssistantTextFromFrameData(frame.Data); delta != "" {
			assistantMessage.WriteString(delta)
		}
		record, _, saveErr := saveThreadRecord(db, threadID, session.roundID, taskID, streamKindMessage, frame.Event, frame.Data, frame.Raw)
		if saveErr != nil {
			status = "failed"
			session.setErr(saveErr)
			log.Logger.Error().Err(saveErr).Str("thread_id", threadID).Msg("save message stream record failed")
			break
		}
		if record != nil {
			session.publish(*record)
		}

		updates := map[string]any{
			"status":     "message_streaming",
			"updated_at": time.Now().UTC(),
		}
		if taskID != "" {
			updates["current_task_id"] = taskID
		}
		_ = db.Model(&orm.AgentThread{}).Where("thread_id = ?", threadID).Updates(updates).Error

		roundUpdates := map[string]any{
			"assistant_message": assistantMessage.String(),
			"status":            "streaming",
			"updated_at":        time.Now().UTC(),
		}
		if taskID != "" {
			roundUpdates["task_id"] = taskID
		}
		_ = db.Model(&orm.AgentThreadRound{}).Where("round_id = ?", session.roundID).Updates(roundUpdates).Error

	}

	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC(),
	}
	_ = db.Model(&orm.AgentThread{}).Where("thread_id = ?", threadID).Updates(updates).Error
	_ = db.Model(&orm.AgentThreadRound{}).Where("round_id = ?", session.roundID).Updates(map[string]any{
		"assistant_message": assistantMessage.String(),
		"status":            status,
		"updated_at":        time.Now().UTC(),
	}).Error
}

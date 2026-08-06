package subagent

import "sync"

const taskLiveSubscriberBuffer = 256

// taskLiveEventBroker forwards ephemeral Task events to SSE clients connected to
// this Core process. Redis remains the replay source and the fallback when a
// subscriber is on another process or cannot keep up with the live channel.
type taskLiveEventBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan TaskEvent]struct{}
}

func newTaskLiveEventBroker() *taskLiveEventBroker {
	return &taskLiveEventBroker{
		subscribers: make(map[string]map[chan TaskEvent]struct{}),
	}
}

func (b *taskLiveEventBroker) subscribe(taskID string) (<-chan TaskEvent, func()) {
	ch := make(chan TaskEvent, taskLiveSubscriberBuffer)
	b.mu.Lock()
	if b.subscribers[taskID] == nil {
		b.subscribers[taskID] = make(map[chan TaskEvent]struct{})
	}
	b.subscribers[taskID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers[taskID], ch)
			if len(b.subscribers[taskID]) == 0 {
				delete(b.subscribers, taskID)
			}
			b.mu.Unlock()
		})
	}
}

func (b *taskLiveEventBroker) publish(taskID string, event TaskEvent) {
	b.mu.RLock()
	channels := make([]chan TaskEvent, 0, len(b.subscribers[taskID]))
	for ch := range b.subscribers[taskID] {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- event:
		default:
			// The event is already in the Redis LIST. A slow subscriber will
			// recover it on the next fallback poll instead of blocking the task.
		}
	}
}

func (b *taskLiveEventBroker) subscriberCount(taskID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[taskID])
}

var taskLiveEvents = newTaskLiveEventBroker()

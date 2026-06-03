package observability

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

type Labels map[string]string

type Counter struct {
	Name   string
	Labels Labels
	Value  int64
}

type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
}

func NewRegistry() *Registry {
	return &Registry{counters: map[string]*Counter{}}
}

func (r *Registry) Inc(name string, labels Labels) {
	r.Add(name, labels, 1)
}

func (r *Registry) Add(name string, labels Labels, delta int64) {
	if r == nil || name == "" || delta == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := metricKey(name, labels)
	counter := r.counters[key]
	if counter == nil {
		counter = &Counter{Name: name, Labels: cloneLabels(labels)}
		r.counters[key] = counter
	}
	counter.Value += delta
}

func (r *Registry) Snapshot() []Counter {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Counter, 0, len(r.counters))
	for _, counter := range r.counters {
		items = append(items, Counter{Name: counter.Name, Labels: cloneLabels(counter.Labels), Value: counter.Value})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return labelsString(items[i].Labels) < labelsString(items[j].Labels)
	})
	return items
}

func (r *Registry) Write(w io.Writer) error {
	return WritePrometheus(w, r.Snapshot())
}

func WritePrometheus(w io.Writer, counters []Counter) error {
	for _, counter := range counters {
		if _, err := fmt.Fprintf(w, "%s%s %d\n", counter.Name, prometheusLabels(counter.Labels), counter.Value); err != nil {
			return err
		}
	}
	return nil
}

func metricKey(name string, labels Labels) string {
	return name + "\xff" + labelsString(labels)
}

func labelsString(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(labels[key])
		b.WriteByte('\xff')
	}
	return b.String()
}

func prometheusLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapeLabel(labels[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func cloneLabels(labels Labels) Labels {
	if labels == nil {
		return nil
	}
	out := make(Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

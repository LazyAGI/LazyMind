package executor

import (
	"sort"
	"sync"
)

// HostRegistry records the capabilities advertised by remote executor hosts.
// It never contains an executable implementation; execution happens through
// the remote Attempt protocol selected by the persisted controller_host.
type HostRegistry struct {
	mu            sync.RWMutex
	registrations map[string]HostRegistration
}

type HostRegistration struct {
	Capabilities         map[string]bool
	AllowAllCapabilities bool
	AllowLegacyTools     bool
}

var DefaultHostRegistry = NewHostRegistry()

func NewHostRegistry() *HostRegistry {
	return &HostRegistry{registrations: map[string]HostRegistration{}}
}

func (r *HostRegistry) RegisterHost(host string, registration HostRegistration) {
	if host == "" {
		panic("workflow Host registration requires host")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registrations[host] = registration
}

func (r *HostRegistry) Hosts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hosts := make([]string, 0, len(r.registrations))
	for host := range r.registrations {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func (r *HostRegistry) Supports(host string, capabilities, legacyTools []string) (bool, []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.registrations[host]
	if !ok {
		return false, append(append([]string{}, capabilities...), legacyTools...)
	}
	missing := []string{}
	for _, capability := range capabilities {
		if !registration.AllowAllCapabilities && !registration.Capabilities[capability] {
			missing = append(missing, capability)
		}
	}
	if !registration.AllowLegacyTools {
		missing = append(missing, legacyTools...)
	}
	return len(missing) == 0, missing
}

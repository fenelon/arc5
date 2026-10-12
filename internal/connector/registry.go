package connector

import (
	"sort"
	"sync"
)

// Factory builds a fresh connector for one run.
type Factory func() Connector

// Registry maps connector names to factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// Default is the registry connector packages add themselves to.
var Default = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register adds a connector. It panics on an empty or duplicate name, since
// both are programming errors caught at startup.
func (r *Registry) Register(name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		panic("connector: empty name")
	}
	if _, dup := r.factories[name]; dup {
		panic("connector: " + name + " registered twice")
	}
	r.factories[name] = f
}

func (r *Registry) New(name string) (Connector, bool) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return f(), true
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

package connector

import (
	"sort"
	"sync"
)

// Display says how a connector's sources are shown.
type Display string

const (
	DisplayChats Display = "chats" // messengers: conversations beside their messages
	DisplayMail  Display = "mail"  // mailboxes: folders and threads
)

// Info describes a kind of connector.
type Info struct {
	Name    string // stored on each account, such as "telegram"
	Title   string // shown to the owner, such as "Telegram"
	Display Display
	Hidden  bool // not offered when the owner adds a source, such as the demo
}

// Factory builds a fresh connector for one run.
type Factory func() Connector

type registered struct {
	info    Info
	factory Factory
}

// Registry holds the connectors built into this binary.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]registered
}

// Default is the registry connector packages add themselves to.
var Default = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{entries: map[string]registered{}}
}

// Register adds a connector. It panics on incomplete info or a duplicate
// name, since both are programming errors caught at startup.
func (r *Registry) Register(info Info, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case info.Name == "" || info.Title == "":
		panic("connector: Info needs a name and a title")
	case info.Display != DisplayChats && info.Display != DisplayMail:
		panic("connector: " + info.Name + " has no display type")
	}
	if _, dup := r.entries[info.Name]; dup {
		panic("connector: " + info.Name + " registered twice")
	}
	r.entries[info.Name] = registered{info: info, factory: f}
}

func (r *Registry) New(name string) (Connector, bool) {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return e.factory(), true
}

func (r *Registry) Info(name string) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e.info, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Infos describes every registered connector, sorted by title.
func (r *Registry) Infos() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

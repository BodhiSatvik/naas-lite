package naas

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrExists means a namespace with that name already exists.
	ErrExists = errors.New("namespace already exists")
	// ErrNotFound means there is no namespace with that name.
	ErrNotFound = errors.New("namespace not found")
)

// Store keeps namespaces in memory. It is safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	items map[string]Namespace
	now   func() time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{items: map[string]Namespace{}, now: time.Now}
}

// Create stores a validated request as a new namespace.
func (s *Store) Create(r Request) (Namespace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[r.Name]; ok {
		return Namespace{}, ErrExists
	}
	ns := Namespace{Request: r, CreatedAt: s.now().UTC()}
	s.items[r.Name] = ns
	return ns, nil
}

// Get returns the namespace with the given name.
func (s *Store) Get(name string) (Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns, ok := s.items[name]
	if !ok {
		return Namespace{}, ErrNotFound
	}
	return ns, nil
}

// List returns all namespaces, sorted by name.
func (s *Store) List() []Namespace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Namespace, 0, len(s.items))
	for _, ns := range s.items {
		out = append(out, ns)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

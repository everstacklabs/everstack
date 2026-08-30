package authz

import (
	"context"
	"strings"
	"sync"
)

// TupleStore persists relationship tuples and answers the one query the engine
// needs: "who are the subjects of object#relation?".
type TupleStore interface {
	// Write inserts tuples (idempotent: re-writing an existing tuple is a no-op).
	Write(ctx context.Context, tuples ...Tuple) error
	// Delete removes tuples (missing tuples are ignored).
	Delete(ctx context.Context, tuples ...Tuple) error
	// DeleteObject removes EVERY tuple in which object is the object (all
	// relations + all subjects), e.g. when the underlying resource is deleted,
	// so no parent link or grant is left orphaned.
	DeleteObject(ctx context.Context, object Object) error
	// ListSubjects returns every subject directly granted relation on object.
	// Userset subjects are returned as-is; the engine expands them.
	ListSubjects(ctx context.Context, object Object, relation string) ([]Subject, error)
}

// MemStore is an in-memory TupleStore for tests, single-tenant defaults, and as
// a cache backing. Safe for concurrent use. Entries are namespaced by the tenant
// in ctx (ContextWithTenant), so reads under one tenant never see another's.
type MemStore struct {
	mu sync.RWMutex
	// key: tenant\x00object#relation -> set of subject strings -> Subject
	m map[string]map[string]Subject
}

// NewMemStore creates an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{m: make(map[string]map[string]Subject)}
}

func memKey(tenant string, obj Object, rel string) string {
	return tenant + "\x00" + obj.String() + "#" + rel
}

// Write implements TupleStore.
func (s *MemStore) Write(ctx context.Context, tuples ...Tuple) error {
	tenant := TenantFromContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tuples {
		k := memKey(tenant, t.Object, t.Relation)
		if s.m[k] == nil {
			s.m[k] = make(map[string]Subject)
		}
		s.m[k][t.Subject.String()] = t.Subject
	}
	return nil
}

// Delete implements TupleStore.
func (s *MemStore) Delete(ctx context.Context, tuples ...Tuple) error {
	tenant := TenantFromContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tuples {
		k := memKey(tenant, t.Object, t.Relation)
		if s.m[k] != nil {
			delete(s.m[k], t.Subject.String())
			if len(s.m[k]) == 0 {
				delete(s.m, k)
			}
		}
	}
	return nil
}

// DeleteObject implements TupleStore.
func (s *MemStore) DeleteObject(ctx context.Context, object Object) error {
	tenant := TenantFromContext(ctx)
	prefix := tenant + "\x00" + object.String() + "#"
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			delete(s.m, k)
		}
	}
	return nil
}

// ListSubjects implements TupleStore.
func (s *MemStore) ListSubjects(ctx context.Context, object Object, relation string) ([]Subject, error) {
	tenant := TenantFromContext(ctx)
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.m[memKey(tenant, object, relation)]
	out := make([]Subject, 0, len(set))
	for _, subj := range set {
		out = append(out, subj)
	}
	return out, nil
}

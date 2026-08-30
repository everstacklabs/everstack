package authz

import (
	"context"
	"fmt"
)

// maxDepth bounds rewrite recursion. The Everstack hierarchy is shallow
// (resource -> instance -> workspace -> organization, ~4 levels with computed
// hops), so 32 is generous while still stopping pathological cycles.
const maxDepth = 32

// Engine evaluates Check queries against a schema and a tuple store. It is the
// Policy Decision Point (PDP). Construct once at startup and share; it is
// stateless beyond its schema + store references.
type Engine struct {
	schema *Schema
	store  TupleStore
}

// NewEngine builds an engine. If schema is nil, the Everstack schema is used.
func NewEngine(store TupleStore, schema *Schema) *Engine {
	if schema == nil {
		schema = EverstackSchema()
	}
	return &Engine{schema: schema, store: store}
}

// Schema returns the engine's schema (for introspection/expansion).
func (e *Engine) Schema() *Schema { return e.schema }

// Store returns the engine's tuple store (for writing membership tuples).
func (e *Engine) Store() TupleStore { return e.store }

// Check reports whether userID has relation on object. relation may be a
// membership relation (owner/admin/member/viewer/editor) or a permission
// relation (can_*). This is the single authorization primitive.
func (e *Engine) Check(ctx context.Context, userID, relation string, object Object) (bool, error) {
	if userID == "" {
		return false, nil
	}
	if object.Type == "" || object.ID == "" {
		// An empty/unscoped object must never authorize. Object resolvers already
		// drop empty-id requests, but fail closed here too: without this a check
		// against an empty object id would match any "*" (public) subject tuple
		// that happened to be written for the empty object.
		return false, nil
	}
	return e.check(ctx, userID, relation, object, make(map[string]bool), 0)
}

// CheckPermission resolves a coarse Permission to its relation on the object's
// type and checks it. This is the entry point the enforcement interceptor uses.
func (e *Engine) CheckPermission(ctx context.Context, userID string, perm Permission, object Object) (bool, error) {
	rel := permissionRelation(perm)
	if rel == "" {
		return false, fmt.Errorf("authz: permission %q has no relation mapping", perm)
	}
	return e.Check(ctx, userID, rel, object)
}

// permissionRelation maps a coarse Permission to the schema relation that
// encodes it. Keeps the interceptor speaking permissions while the engine
// speaks relations.
func permissionRelation(perm Permission) string {
	switch perm {
	case PermOrgView, PermResourceView, PermStorageRead:
		return "can_view"
	case PermResourceCreate, PermResourceEdit, PermStorageWrite:
		return "can_edit"
	case PermResourceDelete:
		return "can_delete"
	case PermOrgManageMembers:
		return "can_manage_members"
	case PermOrgManageBilling:
		return "can_manage_billing"
	case PermOrgManageWorkspaces:
		return "can_manage_workspaces"
	case PermStorageManage:
		return "can_manage_storage"
	case PermWorkspaceManage:
		// On a workspace object this resolves to workspace admin; org
		// owners/admins inherit it via the parent tupleset.
		return "can_manage_members"
	case PermOrgDelete:
		return "can_delete"
	default:
		return ""
	}
}

func (e *Engine) check(ctx context.Context, userID, relation string, object Object, visited map[string]bool, depth int) (bool, error) {
	if depth > maxDepth {
		return false, fmt.Errorf("authz: max recursion depth exceeded checking %s#%s", object, relation)
	}
	key := object.String() + "#" + relation
	if visited[key] {
		// Cycle in the model/tuples — this branch contributes nothing.
		return false, nil
	}
	visited[key] = true
	defer delete(visited, key)

	rw, ok := e.schema.relation(object.Type, relation)
	if !ok {
		// Unknown type/relation: deny. Unknown relations must never grant access.
		return false, nil
	}

	for _, branch := range rw.Union {
		ok, err := e.evalUserset(ctx, userID, relation, object, branch, visited, depth)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (e *Engine) evalUserset(ctx context.Context, userID, relation string, object Object, u Userset, visited map[string]bool, depth int) (bool, error) {
	switch {
	case u.Computed != "":
		return e.check(ctx, userID, u.Computed, object, visited, depth+1)

	case u.TupleToUserset != nil:
		// Follow the tupleset (e.g. "parent") to related objects, then check the
		// computed relation on each.
		related, err := e.store.ListSubjects(ctx, object, u.TupleToUserset.Tupleset)
		if err != nil {
			return false, err
		}
		for _, rel := range related {
			// A tupleset subject references a related object (relation empty).
			if rel.Relation != "" {
				continue
			}
			ok, err := e.check(ctx, userID, u.TupleToUserset.ComputedUserset, rel.Object, visited, depth+1)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil

	default: // This: direct tuples
		subjects, err := e.store.ListSubjects(ctx, object, relation)
		if err != nil {
			return false, err
		}
		for _, s := range subjects {
			if s.IsUser() {
				if s.Object.ID == userID || s.Object.ID == "*" {
					return true, nil
				}
				continue
			}
			// Userset subject (e.g. organization:acme#member): the user has the
			// relation if they are in that userset.
			ok, err := e.check(ctx, userID, s.Relation, s.Object, visited, depth+1)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
}

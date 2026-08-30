package authz

// This file encodes Everstack's authorization model in the Zanzibar style:
// each type has named relations, and each relation is a rewrite rule describing
// how to decide membership. A user "has" a relation on an object if any branch
// of its rewrite holds.

// Userset is one branch of a rewrite expression.
type Userset struct {
	// This: direct tuples (object#relation@subject) grant the relation.
	This bool
	// Computed: the relation holds if the user has <Computed> on the SAME object
	// (e.g. member is computed from admin).
	Computed string
	// TupleToUserset: follow Tupleset tuples from this object to related
	// objects, then check ComputedUserset on each related object (e.g. a
	// workspace's member is computed from its parent organization's member:
	// {Tupleset: "parent", ComputedUserset: "member"}).
	TupleToUserset *TupleToUserset
}

// TupleToUserset follows a tupleset relation to a parent/related object and
// checks a computed relation there.
type TupleToUserset struct {
	Tupleset        string // relation on this object whose subjects are related objects
	ComputedUserset string // relation to check on each related object
}

// Rewrite is the union of its branches. An empty Union is treated as {This:true}
// (direct tuples only).
type Rewrite struct {
	Union []Userset
}

// TypeDef is a type and its relations.
type TypeDef struct {
	Name      string
	Relations map[string]Rewrite
}

// Schema is the full authorization model.
type Schema struct {
	Types map[string]TypeDef
}

// relation returns the rewrite for a type's relation, defaulting to direct-only.
func (s *Schema) relation(typ, rel string) (Rewrite, bool) {
	t, ok := s.Types[typ]
	if !ok {
		return Rewrite{}, false
	}
	r, ok := t.Relations[rel]
	if !ok {
		return Rewrite{}, false
	}
	if len(r.Union) == 0 {
		return Rewrite{Union: []Userset{{This: true}}}, true
	}
	return r, true
}

// direct is a convenience for a direct-only relation.
func direct() Rewrite { return Rewrite{Union: []Userset{{This: true}}} }

// thisOr builds "direct tuples OR computed relation(s)".
func thisOr(computed ...string) Rewrite {
	u := []Userset{{This: true}}
	for _, c := range computed {
		u = append(u, Userset{Computed: c})
	}
	return Rewrite{Union: u}
}

// fromParent builds "direct tuples OR (the parent's <rel> via the parent
// tupleset) OR optionally computed local relations".
func fromParent(parentRel, computedOnParent string, alsoComputed ...string) Rewrite {
	u := []Userset{
		{This: true},
		{TupleToUserset: &TupleToUserset{Tupleset: parentRel, ComputedUserset: computedOnParent}},
	}
	for _, c := range alsoComputed {
		u = append(u, Userset{Computed: c})
	}
	return Rewrite{Union: u}
}

// EverstackSchema is the authoritative model. The container hierarchy is
// organization -> workspace -> instance -> resource, with roles defined at the
// org and workspace levels and inherited downward via "parent" tuplesets.
//
// Permission relations (can_*) are computed from membership relations, so they
// agree with the coarse matrix in model.go by construction.
func EverstackSchema() *Schema {
	return &Schema{Types: map[string]TypeDef{
		UserType: {Name: UserType, Relations: map[string]Rewrite{}},

		"organization": {Name: "organization", Relations: map[string]Rewrite{
			"owner":  direct(),
			"admin":  thisOr("owner"),
			"member": thisOr("admin"),
			"viewer": thisOr("member"),
			// permissions
			"can_manage_billing":    {Union: []Userset{{Computed: "owner"}}},
			"can_delete":            {Union: []Userset{{Computed: "owner"}}},
			"can_manage_members":    {Union: []Userset{{Computed: "admin"}}},
			"can_manage_workspaces": {Union: []Userset{{Computed: "admin"}}},
			"can_manage_storage":    {Union: []Userset{{Computed: "admin"}}},
			"can_view":              {Union: []Userset{{Computed: "viewer"}}},
			"can_edit":              {Union: []Userset{{Computed: "member"}}},
		}},

		"workspace": {Name: "workspace", Relations: map[string]Rewrite{
			"parent":             direct(), // subject: organization:<id>
			"admin":              fromParent("parent", "admin"),
			"member":             fromParent("parent", "member", "admin"),
			"viewer":             fromParent("parent", "viewer", "member"),
			"can_manage_members": {Union: []Userset{{Computed: "admin"}}},
			"can_manage_storage": {Union: []Userset{{Computed: "admin"}}},
			"can_view":           {Union: []Userset{{Computed: "viewer"}}},
			"can_edit":           {Union: []Userset{{Computed: "member"}}},
			"can_delete":         {Union: []Userset{{Computed: "admin"}}},
		}},

		"instance": {Name: "instance", Relations: map[string]Rewrite{
			"parent":             direct(), // subject: workspace:<id>
			"admin":              fromParent("parent", "admin"),
			"member":             fromParent("parent", "member", "admin"),
			"viewer":             fromParent("parent", "viewer", "member"),
			"can_manage_storage": {Union: []Userset{{Computed: "admin"}}},
			"can_view":           {Union: []Userset{{Computed: "viewer"}}},
			"can_edit":           {Union: []Userset{{Computed: "member"}}},
		}},

		// Generic leaf resource type. Real resource types (dataset, agent,
		// prompt, alert, trace_view, ...) share this shape: an explicit editor/
		// viewer grant OR inheritance from the parent instance. ResourceType()
		// produces one of these per concrete type name.
		"resource": resourceTypeDef("resource"),
	}}
}

// resourceTypeDef builds a leaf resource type definition. manager/editor/viewer
// can be granted directly (per-resource sharing) or inherited from the parent
// instance. Delete requires manager (admin-level), matching the coarse matrix in
// model.go where a member can edit but not delete; the resource creator is
// granted manager so they can still delete their own resource.
func resourceTypeDef(name string) TypeDef {
	return TypeDef{Name: name, Relations: map[string]Rewrite{
		"parent": direct(), // subject: instance:<id>
		// Full control: the parent instance's admins (org owners/admins) plus any
		// explicit manager grant (e.g. the creator).
		"manager": fromParent("parent", "admin"),
		// Read-write: managers, the parent instance's members, or an explicit grant.
		"editor":     fromParent("parent", "member", "manager"),
		"viewer":     fromParent("parent", "viewer", "editor"),
		"can_view":   {Union: []Userset{{Computed: "viewer"}}},
		"can_edit":   {Union: []Userset{{Computed: "editor"}}},
		"can_delete": {Union: []Userset{{Computed: "manager"}}},
	}}
}

// DefaultResourceTypes are the concrete resource type names registered in the
// authorization model. The enforcement engine (PEP) and the BatchCheck endpoint
// must register the SAME set so a procedure's check and a frontend gate agree.
func DefaultResourceTypes() []string {
	return []string{"dataset", "agent", "prompt", "alert", "trace_view"}
}

// WithResourceTypes returns a copy of the schema with concrete resource type
// names registered (each sharing the generic resource shape). Call once at
// startup with the real type names.
func (s *Schema) WithResourceTypes(names ...string) *Schema {
	out := &Schema{Types: make(map[string]TypeDef, len(s.Types)+len(names))}
	for k, v := range s.Types {
		out.Types[k] = v
	}
	for _, n := range names {
		out.Types[n] = resourceTypeDef(n)
	}
	return out
}

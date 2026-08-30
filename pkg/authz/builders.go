package authz

// Convenience constructors for the common objects and tuples, so callers
// (membership sync, resource creation, backfill) never hand-format strings.

// Org returns an organization object.
func Org(id string) Object { return Object{Type: "organization", ID: id} }

// Workspace returns a workspace object.
func Workspace(id string) Object { return Object{Type: "workspace", ID: id} }

// Instance returns an instance object.
func Instance(id string) Object { return Object{Type: "instance", ID: id} }

// Resource returns a resource object of a concrete type (dataset, agent, ...).
func Resource(typ, id string) Object { return Object{Type: typ, ID: id} }

// OrgMembership builds the tuple granting a user a role on an organization.
// role must be one of owner/admin/member/viewer (the relation name).
func OrgMembership(orgID string, userID string, role Role) Tuple {
	return NewTuple(Org(orgID), string(role), User(userID))
}

// WorkspaceMembership builds the tuple granting a user a role on a workspace.
func WorkspaceMembership(workspaceID string, userID string, role Role) Tuple {
	return NewTuple(Workspace(workspaceID), string(role), User(userID))
}

// WorkspaceParent links a workspace to its organization (enables inheritance).
func WorkspaceParent(workspaceID, orgID string) Tuple {
	return NewTuple(Workspace(workspaceID), "parent", Subject{Object: Org(orgID)})
}

// InstanceParent links an instance to its workspace.
func InstanceParent(instanceID, workspaceID string) Tuple {
	return NewTuple(Instance(instanceID), "parent", Subject{Object: Workspace(workspaceID)})
}

// ResourceParent links a resource to its instance.
func ResourceParent(resourceType, resourceID, instanceID string) Tuple {
	return NewTuple(Resource(resourceType, resourceID), "parent", Subject{Object: Instance(instanceID)})
}

// ResourceGrant grants a user a direct role (editor/viewer) on a resource —
// per-resource sharing on top of inherited access.
func ResourceGrant(resourceType, resourceID, userID string, relation string) Tuple {
	return NewTuple(Resource(resourceType, resourceID), relation, User(userID))
}

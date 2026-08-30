// Package storageauth defines the authorization vocabulary shared by storage
// transports, background jobs, workspace services, and projections.
package storageauth

import "github.com/everstacklabs/everstack/pkg/authz"

// Action is one server-side storage operation that requires an explicit
// permission decision.
type Action string

const (
	ActionConnectionConfigure Action = "storage.connection.configure"
	ActionConnectionRead      Action = "storage.connection.read"
	ActionConnectionUpdate    Action = "storage.connection.update"
	ActionConnectionDelete    Action = "storage.connection.delete"

	ActionUploadInitiate Action = "storage.upload.initiate"
	ActionUploadRead     Action = "storage.upload.read"
	ActionUploadComplete Action = "storage.upload.complete"
	ActionUploadProxy    Action = "storage.upload.proxy"
	ActionUploadInternal Action = "storage.upload.internal"

	ActionObjectDownload Action = "storage.object.download"
	ActionObjectDelete   Action = "storage.object.delete"
	ActionObjectList     Action = "storage.object.list"
	ActionUsageRead      Action = "storage.usage.read"
	ActionUsageUpdate    Action = "storage.usage.update"

	ActionWorkspaceRead     Action = "storage.workspace.read"
	ActionWorkspaceWrite    Action = "storage.workspace.write"
	ActionCheckpointCreate  Action = "storage.checkpoint.create"
	ActionCheckpointRestore Action = "storage.checkpoint.restore"
	ActionWorkspaceFork     Action = "storage.workspace.fork"
	ActionArtifactPromote   Action = "storage.artifact.promote"

	ActionAdminSync      Action = "storage.admin.sync"
	ActionAdminReconcile Action = "storage.admin.reconcile"
)

var allActions = []Action{
	ActionConnectionConfigure,
	ActionConnectionRead,
	ActionConnectionUpdate,
	ActionConnectionDelete,
	ActionUploadInitiate,
	ActionUploadRead,
	ActionUploadComplete,
	ActionUploadProxy,
	ActionUploadInternal,
	ActionObjectDownload,
	ActionObjectDelete,
	ActionObjectList,
	ActionUsageRead,
	ActionUsageUpdate,
	ActionWorkspaceRead,
	ActionWorkspaceWrite,
	ActionCheckpointCreate,
	ActionCheckpointRestore,
	ActionWorkspaceFork,
	ActionArtifactPromote,
	ActionAdminSync,
	ActionAdminReconcile,
}

// AllActions returns the complete action catalog. Callers receive a copy so
// the authorization vocabulary cannot be mutated at runtime.
func AllActions() []Action {
	return append([]Action(nil), allActions...)
}

// PermissionFor maps an action to its coarse tenant permission. Unknown
// actions intentionally have no permission and must be denied by callers.
func PermissionFor(action Action) (authz.Permission, bool) {
	switch action {
	case ActionConnectionRead, ActionUploadRead, ActionObjectDownload, ActionObjectList, ActionUsageRead, ActionWorkspaceRead:
		return authz.PermStorageRead, true
	case ActionUploadInitiate, ActionUploadComplete, ActionUploadProxy, ActionUploadInternal,
		ActionObjectDelete, ActionUsageUpdate, ActionWorkspaceWrite, ActionCheckpointCreate, ActionCheckpointRestore,
		ActionWorkspaceFork, ActionArtifactPromote:
		return authz.PermStorageWrite, true
	case ActionConnectionConfigure, ActionConnectionUpdate, ActionConnectionDelete, ActionAdminSync, ActionAdminReconcile:
		return authz.PermStorageManage, true
	default:
		return "", false
	}
}

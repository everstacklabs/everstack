import type { Permission } from "./model";

// PermissionSet wraps the per-resource permission snapshot the backend returns
// from its batch-check / ListUserPermissions endpoint. This is the fine-grained
// PDP mirror: the UI asks the SAME engine the backend enforces with, so gates
// cannot drift from enforcement.
//
// Keys are "permission" for object-less checks or "permission@object" (e.g.
// "resource:edit@dataset:42") for per-resource checks, matching the backend's
// CheckPermission(perm, object) vocabulary.
export class PermissionSet {
  private readonly granted: ReadonlySet<string>;

  constructor(granted: Iterable<string>) {
    this.granted = new Set(granted);
  }

  /** has reports whether a (permission, object?) is granted by the backend. */
  has(perm: Permission, object?: string): boolean {
    if (object) return this.granted.has(`${perm}@${object}`);
    return this.granted.has(perm);
  }

  /** size is the number of granted entries (for debugging/telemetry). */
  get size(): number {
    return this.granted.size;
  }
}

/** Empty snapshot — denies everything. Use as a safe default before load. */
export const EMPTY_PERMISSIONS = new PermissionSet([]);

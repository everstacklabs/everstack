/**
 * trace-tree-flatten: turn a span tree into a flat list of visible rows (B5).
 *
 * The recursive tree renderer is fine for ordinary traces but a large agent run
 * (thousands of spans) renders every node at once and janks the browser. A
 * virtualizer needs a flat, index-addressable list of exactly the rows that are
 * currently visible (respecting collapse state). This computes that list, plus
 * the depth / isLast / hasChildren metadata each row needs, without any
 * dependency on the proto type so it is trivially testable.
 */

export interface TreeNodeLike<T> {
  span?: { spanId?: string } & T
  children?: TreeNodeLike<T>[]
}

export interface FlatRow<T> {
  spanId: string
  span: { spanId?: string } & T
  depth: number
  /** True when this node is the last child of its parent (for connector lines). */
  isLast: boolean
  hasChildren: boolean
}

/**
 * Depth-first flatten of the visible nodes. A node's children are included only
 * when the node is expanded (`expandedMap.get(spanId) ?? true`, matching the
 * tree view's default-expanded behavior). Nodes without a spanId are skipped
 * but their subtree is still traversed so structure is preserved.
 */
export function flattenSpanTree<T>(
  root: TreeNodeLike<T> | null | undefined,
  expandedMap: Map<string, boolean>,
): FlatRow<T>[] {
  const rows: FlatRow<T>[] = []
  if (!root) return rows

  const walk = (node: TreeNodeLike<T>, depth: number, isLast: boolean) => {
    const span = node.span
    const children = node.children ?? []
    const hasChildren = children.length > 0
    const spanId = span?.spanId

    if (span && spanId) {
      rows.push({ spanId, span, depth, isLast, hasChildren })
      const expanded = expandedMap.get(spanId) ?? true
      if (hasChildren && expanded) {
        children.forEach((child, i) => walk(child, depth + 1, i === children.length - 1))
      }
    } else if (hasChildren) {
      // Node with no span id: keep traversing at the same depth so we do not
      // lose its descendants, but do not emit a row for it.
      children.forEach((child, i) => walk(child, depth, i === children.length - 1))
    }
  }

  walk(root, 0, true)
  return rows
}

/** Total number of span nodes in the tree, regardless of expand state. */
export function countSpanNodes<T>(root: TreeNodeLike<T> | null | undefined): number {
  if (!root) return 0
  let n = root.span?.spanId ? 1 : 0
  for (const child of root.children ?? []) n += countSpanNodes(child)
  return n
}

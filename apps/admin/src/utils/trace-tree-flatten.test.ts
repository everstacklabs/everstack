import { describe, it, expect } from 'vitest'
import { flattenSpanTree, countSpanNodes, type TreeNodeLike } from './trace-tree-flatten'

type N = TreeNodeLike<Record<string, unknown>>

const node = (id: string, children: N[] = []): N => ({ span: { spanId: id }, children })

const tree: N = node('root', [
  node('a', [node('a1'), node('a2')]),
  node('b', [node('b1')]),
])

describe('flattenSpanTree', () => {
  it('depth-first flattens all visible nodes with depth', () => {
    const rows = flattenSpanTree(tree, new Map())
    expect(rows.map((r) => r.spanId)).toEqual(['root', 'a', 'a1', 'a2', 'b', 'b1'])
    expect(rows.map((r) => r.depth)).toEqual([0, 1, 2, 2, 1, 2])
  })

  it('hides children of collapsed nodes', () => {
    const expanded = new Map<string, boolean>([['a', false]])
    const rows = flattenSpanTree(tree, expanded)
    expect(rows.map((r) => r.spanId)).toEqual(['root', 'a', 'b', 'b1'])
  })

  it('marks isLast and hasChildren correctly', () => {
    const rows = flattenSpanTree(tree, new Map())
    const byId = new Map(rows.map((r) => [r.spanId, r]))
    expect(byId.get('a')!.isLast).toBe(false)
    expect(byId.get('b')!.isLast).toBe(true)
    expect(byId.get('a2')!.isLast).toBe(true)
    expect(byId.get('a')!.hasChildren).toBe(true)
    expect(byId.get('a1')!.hasChildren).toBe(false)
  })

  it('skips a node without a span id but keeps its descendants at the same depth', () => {
    const t: N = { children: [node('x'), node('y')] }
    const rows = flattenSpanTree(t, new Map())
    expect(rows.map((r) => r.spanId)).toEqual(['x', 'y'])
    expect(rows.every((r) => r.depth === 0)).toBe(true)
  })

  it('returns [] for a null root', () => {
    expect(flattenSpanTree(null, new Map())).toEqual([])
  })
})

describe('countSpanNodes', () => {
  it('counts every node regardless of expand state', () => {
    expect(countSpanNodes(tree)).toBe(6)
  })

  it('returns 0 for a null root', () => {
    expect(countSpanNodes(null)).toBe(0)
  })
})

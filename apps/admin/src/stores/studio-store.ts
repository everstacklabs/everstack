import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import {
  applyNodeChanges,
  applyEdgeChanges,
  addEdge,
  type NodeChange,
  type EdgeChange,
  type Connection,
} from '@xyflow/react'
import type { StudioNode, StudioEdge, StudioNodeType, StudioNodeData, NodeConfig } from '@/components/deployments/studio/types'
import { NODE_REGISTRY, getDefaultConfig } from '@/components/deployments/studio/node-registry'

interface HistorySnapshot {
  nodes: StudioNode[]
  edges: StudioEdge[]
}

interface PublishedSnapshot {
  nodes: StudioNode[]
  edges: StudioEdge[]
  name: string
  description: string
  viewport: { x: number; y: number; zoom: number }
  enabled: boolean
  version: number
  variables: Record<string, string>
}

interface HandlePickerState {
  nodeId: string
  handleId: string
  screenPosition: { x: number; y: number }
}

interface StudioState {
  // Workflow data
  workflowId: string | null
  tenantId: string
  name: string
  description: string
  nodes: StudioNode[]
  edges: StudioEdge[]
  viewport: { x: number; y: number; zoom: number }
  enabled: boolean
  version: number

  // UI state
  selectedNodeId: string | null
  hoveredEdgeId: string | null
  pendingEdgeInsertId: string | null
  isConfigPanelOpen: boolean
  isPaletteCollapsed: boolean
  isVariablesPanelOpen: boolean
  handlePickerState: HandlePickerState | null

  // Variables
  variables: Record<string, string>           // persisted with workflow
  sessionVariables: Record<string, string>    // session-only overrides, cleared on reload

  // Published snapshot (for auto-draft)
  publishedSnapshot: PublishedSnapshot | null

  // History
  past: HistorySnapshot[]
  future: HistorySnapshot[]

  // Actions
  setWorkflowData: (data: {
    id: string
    tenantId?: string
    name: string
    description?: string
    nodes: StudioNode[]
    edges: StudioEdge[]
    viewport?: { x: number; y: number; zoom: number }
    enabled: boolean
    version: number
    variables?: Record<string, string>
  }) => void
  addNode: (type: StudioNodeType, position: { x: number; y: number }) => void
  removeNode: (id: string) => void
  duplicateNode: (id: string) => void
  updateNodeConfig: (id: string, config: NodeConfig) => void
  updateNodeLabel: (id: string, label: string) => void
  selectNode: (id: string | null) => void
  setHoveredEdgeId: (id: string | null) => void
  setPendingEdgeInsert: (edgeId: string | null) => void
  onNodesChange: (changes: NodeChange<StudioNode>[]) => void
  onEdgesChange: (changes: EdgeChange<StudioEdge>[]) => void
  onConnect: (connection: Connection) => void
  setName: (name: string) => void
  setDescription: (description: string) => void
  setViewport: (viewport: { x: number; y: number; zoom: number }) => void
  togglePalette: () => void
  removeEdge: (edgeId: string) => void
  insertNodeOnEdge: (edgeId: string, nodeType: StudioNodeType) => void
  openHandlePicker: (nodeId: string, handleId: string, screenPosition: { x: number; y: number }) => void
  closeHandlePicker: () => void
  addNodeFromHandle: (nodeType: StudioNodeType) => void
  undo: () => void
  redo: () => void
  pushHistory: () => void
  setVariable: (key: string, value: string) => void
  removeVariable: (key: string) => void
  setSessionVariable: (key: string, value: string) => void
  removeSessionVariable: (key: string) => void
  clearSessionVariables: () => void
  setVariablesPanelOpen: (open: boolean) => void
  cancelDraftChanges: () => void
  clearPublishedSnapshot: () => void
  reset: () => void
}

let isDragging = false
let nodeIdCounter = 0
function generateNodeId(): string {
  nodeIdCounter += 1
  return `node_${Date.now()}_${nodeIdCounter}`
}

function generateEdgeId(): string {
  return `edge_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
}

export const useStudioStore = create<StudioState>()(
  devtools(
    (set, get) => ({
      // Initial state
      workflowId: null,
      tenantId: '',
      name: 'Untitled Workflow',
      description: '',
      nodes: [],
      edges: [],
      viewport: { x: 0, y: 0, zoom: 1 },
      enabled: false,
      version: 1,
      selectedNodeId: null,
      hoveredEdgeId: null,
      pendingEdgeInsertId: null,
      isConfigPanelOpen: false,
      isPaletteCollapsed: false,
      isVariablesPanelOpen: false,
      handlePickerState: null,
      publishedSnapshot: null,
      variables: {},
      sessionVariables: {},
      past: [],
      future: [],

      setWorkflowData: (data) =>
        set((prev) => ({
          workflowId: data.id,
          tenantId: data.tenantId ?? prev.tenantId,
          name: data.name,
          description: data.description ?? '',
          nodes: data.nodes,
          edges: data.edges,
          viewport: data.viewport ?? { x: 0, y: 0, zoom: 1 },
          enabled: data.enabled,
          version: data.version,
          variables: data.variables ?? prev.variables,
          publishedSnapshot: null,
          past: [],
          future: [],
        })),

      addNode: (type, position) => {
        const state = get()
        const meta = NODE_REGISTRY[type]

        // Check maxInstances
        if (meta.maxInstances) {
          const count = state.nodes.filter(
            (n) => (n.data as StudioNodeData).nodeType === type
          ).length
          if (count >= meta.maxInstances) return
        }

        const id = generateNodeId()
        const newNode: StudioNode = {
          id,
          type: 'studioNode',
          position,
          data: {
            nodeType: type,
            label: meta.label,
            config: getDefaultConfig(type) as NodeConfig,
            isConfigured: false,
          },
        }

        state.pushHistory()
        set({
          nodes: [...state.nodes, newNode],
          future: [],
          selectedNodeId: id,
          isConfigPanelOpen: true,
        })
      },

      removeNode: (id) => {
        const state = get()
        state.pushHistory()
        set({
          nodes: state.nodes.filter((n) => n.id !== id),
          edges: state.edges.filter((e) => e.source !== id && e.target !== id),
          selectedNodeId: state.selectedNodeId === id ? null : state.selectedNodeId,
          isConfigPanelOpen: state.selectedNodeId === id ? false : state.isConfigPanelOpen,
          future: [],
        })
      },

      duplicateNode: (id) => {
        const state = get()
        const sourceNode = state.nodes.find((n) => n.id === id)
        if (!sourceNode) return

        const sourceData = sourceNode.data as StudioNodeData
        const newId = generateNodeId()
        const newNode: StudioNode = {
          id: newId,
          type: 'studioNode',
          position: {
            x: sourceNode.position.x + 50,
            y: sourceNode.position.y + 50,
          },
          data: {
            ...JSON.parse(JSON.stringify(sourceData)),
            label: `${sourceData.label} (copy)`,
          },
        }

        state.pushHistory()
        set({
          nodes: [...state.nodes, newNode],
          future: [],
          selectedNodeId: newId,
          isConfigPanelOpen: true,
        })
      },

      updateNodeConfig: (id, config) => {
        const state = get()
        state.pushHistory()
        set({
          nodes: state.nodes.map((n) =>
            n.id === id
              ? { ...n, data: { ...n.data, config, isConfigured: true } }
              : n
          ),
          future: [],
        })
      },

      updateNodeLabel: (id, label) => {
        const state = get()
        state.pushHistory()
        set({
          nodes: state.nodes.map((n) =>
            n.id === id ? { ...n, data: { ...n.data, label } } : n
          ),
          future: [],
        })
      },

      selectNode: (id) =>
        set({
          selectedNodeId: id,
          isConfigPanelOpen: id !== null,
        }),

      setHoveredEdgeId: (id) => set({ hoveredEdgeId: id }),

      setPendingEdgeInsert: (edgeId) =>
        set({
          pendingEdgeInsertId: edgeId,
          isPaletteCollapsed: edgeId !== null ? false : get().isPaletteCollapsed,
        }),

      onNodesChange: (changes) => {
        // Selection is managed via onNodeClick/onPaneClick/selectNode,
        // so we only apply non-select changes here to avoid race conditions
        const nonSelectChanges = changes.filter((c) => c.type !== 'select')
        if (nonSelectChanges.length === 0) return

        // Push history once at the start of a drag so the pre-drag
        // position can be restored with undo.
        const hasDragStart = nonSelectChanges.some(
          (c) => c.type === 'position' && c.dragging === true
        )
        const hasDragEnd = nonSelectChanges.some(
          (c) => c.type === 'position' && c.dragging === false
        )

        if (hasDragStart && !isDragging) {
          isDragging = true
          get().pushHistory()
        }
        if (hasDragEnd) {
          isDragging = false
        }

        set({
          nodes: applyNodeChanges(nonSelectChanges, get().nodes),
          ...(hasDragEnd ? { future: [] } : {}),
        })
      },

      onEdgesChange: (changes) => {
        set({ edges: applyEdgeChanges(changes, get().edges) })
      },

      onConnect: (connection) => {
        const state = get()
        state.pushHistory()
        const newEdge: StudioEdge = {
          ...connection,
          id: generateEdgeId(),
          source: connection.source,
          target: connection.target,
          type: 'studioEdge',
        }
        set({ edges: addEdge(newEdge, state.edges), future: [] })
      },

      setName: (name) => set({ name }),
      setDescription: (description) => set({ description }),
      setViewport: (viewport) => set({ viewport }),
      togglePalette: () => set((s) => ({ isPaletteCollapsed: !s.isPaletteCollapsed })),

      removeEdge: (edgeId) => {
        const state = get()
        state.pushHistory()
        set({
          edges: state.edges.filter((e) => e.id !== edgeId),
          future: [],
        })
      },

      insertNodeOnEdge: (edgeId, nodeType) => {
        const state = get()
        const edge = state.edges.find((e) => e.id === edgeId)
        if (!edge) return

        const meta = NODE_REGISTRY[nodeType]

        // Check maxInstances
        if (meta.maxInstances) {
          const count = state.nodes.filter(
            (n) => (n.data as StudioNodeData).nodeType === nodeType
          ).length
          if (count >= meta.maxInstances) return
        }

        const sourceNode = state.nodes.find((n) => n.id === edge.source)
        const targetNode = state.nodes.find((n) => n.id === edge.target)
        if (!sourceNode || !targetNode) return

        const NODE_SPACING = 150

        // Place new node at the target's position, shift target and descendants down
        const newNodeX = targetNode.position.x
        const newNodeY = targetNode.position.y

        // Find all downstream descendants of target (BFS) to shift them together
        const downstreamIds = new Set<string>()
        const queue = [edge.target]
        while (queue.length > 0) {
          const current = queue.shift()!
          if (downstreamIds.has(current)) continue
          downstreamIds.add(current)
          for (const e of state.edges) {
            if (e.source === current && !downstreamIds.has(e.target)) {
              queue.push(e.target)
            }
          }
        }

        // Always shift target and all its descendants down
        const updatedNodes = state.nodes.map((n) =>
          downstreamIds.has(n.id)
            ? { ...n, position: { ...n.position, y: n.position.y + NODE_SPACING } }
            : n
        )

        const newNodeId = generateNodeId()
        const newNode: StudioNode = {
          id: newNodeId,
          type: 'studioNode',
          position: { x: newNodeX, y: newNodeY },
          data: {
            nodeType,
            label: meta.label,
            config: getDefaultConfig(nodeType) as NodeConfig,
            isConfigured: false,
          },
        }

        // Look up actual handle IDs from the new node's registry entry
        const newNodeTargetHandle = meta.handles.find((h) => h.type === 'target')?.id
        const newNodeSourceHandle = meta.handles.find((h) => h.type === 'source')?.id

        // Create two new edges: source→new and new→target
        const edgeToNew: StudioEdge = {
          id: generateEdgeId(),
          source: edge.source,
          target: newNodeId,
          sourceHandle: edge.sourceHandle ?? undefined,
          targetHandle: newNodeTargetHandle,
          type: 'studioEdge',
        }
        const edgeFromNew: StudioEdge = {
          id: generateEdgeId(),
          source: newNodeId,
          target: edge.target,
          sourceHandle: newNodeSourceHandle,
          targetHandle: edge.targetHandle ?? undefined,
          type: 'studioEdge',
        }

        state.pushHistory()
        set({
          nodes: [...updatedNodes, newNode],
          edges: [
            ...state.edges.filter((e) => e.id !== edgeId),
            edgeToNew,
            edgeFromNew,
          ],
          future: [],
          selectedNodeId: newNodeId,
          isConfigPanelOpen: true,
          pendingEdgeInsertId: null,
        })
      },

      openHandlePicker: (nodeId, handleId, screenPosition) =>
        set({ handlePickerState: { nodeId, handleId, screenPosition } }),

      closeHandlePicker: () => set({ handlePickerState: null }),

      addNodeFromHandle: (nodeType) => {
        const state = get()
        const picker = state.handlePickerState
        if (!picker) return

        const meta = NODE_REGISTRY[nodeType]

        // Check maxInstances
        if (meta.maxInstances) {
          const count = state.nodes.filter(
            (n) => (n.data as StudioNodeData).nodeType === nodeType
          ).length
          if (count >= meta.maxInstances) return
        }

        // Check if the handle already has a connection
        const existingEdge = state.edges.find(
          (e) => e.source === picker.nodeId && e.sourceHandle === picker.handleId
        )

        if (existingEdge) {
          // Delegate to insertNodeOnEdge logic
          state.closeHandlePicker()
          state.insertNodeOnEdge(existingEdge.id, nodeType)
          return
        }

        // No existing edge: create node below and auto-connect
        const sourceNode = state.nodes.find((n) => n.id === picker.nodeId)
        if (!sourceNode) return

        const newNodeId = generateNodeId()
        const newNode: StudioNode = {
          id: newNodeId,
          type: 'studioNode',
          position: {
            x: sourceNode.position.x,
            y: sourceNode.position.y + 150,
          },
          data: {
            nodeType,
            label: meta.label,
            config: getDefaultConfig(nodeType) as NodeConfig,
            isConfigured: false,
          },
        }

        const newNodeTargetHandle = meta.handles.find((h) => h.type === 'target')?.id

        const newEdge: StudioEdge = {
          id: generateEdgeId(),
          source: picker.nodeId,
          target: newNodeId,
          sourceHandle: picker.handleId,
          targetHandle: newNodeTargetHandle,
          type: 'studioEdge',
        }

        state.pushHistory()
        set({
          nodes: [...state.nodes, newNode],
          edges: [...state.edges, newEdge],
          future: [],
          selectedNodeId: newNodeId,
          isConfigPanelOpen: true,
          handlePickerState: null,
        })
      },

      pushHistory: () => {
        const state = get()
        // On first mutation of a published workflow, capture snapshot + auto-draft
        if (state.enabled && !state.publishedSnapshot) {
          set({
            publishedSnapshot: {
              nodes: JSON.parse(JSON.stringify(state.nodes)),
              edges: JSON.parse(JSON.stringify(state.edges)),
              name: state.name,
              description: state.description,
              viewport: { ...state.viewport },
              enabled: true,
              version: state.version,
              variables: { ...state.variables },
            },
            enabled: false,
          })
        }
        const snapshot: HistorySnapshot = {
          nodes: JSON.parse(JSON.stringify(state.nodes)),
          edges: JSON.parse(JSON.stringify(state.edges)),
        }
        const newPast = [...state.past, snapshot]
        if (newPast.length > 50) newPast.shift()
        set({ past: newPast })
      },

      undo: () => {
        const { past, nodes, edges } = get()
        if (past.length === 0) return
        const prev = past[past.length - 1]
        const newPast = past.slice(0, -1)
        set({
          past: newPast,
          future: [
            { nodes: JSON.parse(JSON.stringify(nodes)), edges: JSON.parse(JSON.stringify(edges)) },
            ...get().future,
          ],
          nodes: prev.nodes,
          edges: prev.edges,
        })
      },

      redo: () => {
        const { future, nodes, edges } = get()
        if (future.length === 0) return
        const next = future[0]
        const newFuture = future.slice(1)
        set({
          future: newFuture,
          past: [
            ...get().past,
            { nodes: JSON.parse(JSON.stringify(nodes)), edges: JSON.parse(JSON.stringify(edges)) },
          ],
          nodes: next.nodes,
          edges: next.edges,
        })
      },

      setVariable: (key, value) =>
        set((s) => ({ variables: { ...s.variables, [key]: value } })),

      removeVariable: (key) =>
        set((s) => {
          const { [key]: _, ...rest } = s.variables
          return { variables: rest }
        }),

      setSessionVariable: (key, value) =>
        set((s) => ({ sessionVariables: { ...s.sessionVariables, [key]: value } })),

      removeSessionVariable: (key) =>
        set((s) => {
          const { [key]: _, ...rest } = s.sessionVariables
          return { sessionVariables: rest }
        }),

      clearSessionVariables: () => set({ sessionVariables: {} }),

      setVariablesPanelOpen: (open) => set({ isVariablesPanelOpen: open }),

      cancelDraftChanges: () => {
        const { publishedSnapshot } = get()
        if (!publishedSnapshot) return
        set({
          nodes: publishedSnapshot.nodes,
          edges: publishedSnapshot.edges,
          name: publishedSnapshot.name,
          description: publishedSnapshot.description,
          viewport: publishedSnapshot.viewport,
          enabled: publishedSnapshot.enabled,
          version: publishedSnapshot.version,
          variables: publishedSnapshot.variables,
          publishedSnapshot: null,
          past: [],
          future: [],
        })
      },

      clearPublishedSnapshot: () => set({ publishedSnapshot: null }),

      reset: () =>
        set({
          workflowId: null,
          tenantId: '',
          name: 'Untitled Workflow',
          description: '',
          nodes: [],
          edges: [],
          viewport: { x: 0, y: 0, zoom: 1 },
          enabled: false,
          version: 1,
          selectedNodeId: null,
          hoveredEdgeId: null,
          pendingEdgeInsertId: null,
          isConfigPanelOpen: false,
          isPaletteCollapsed: false,
          isVariablesPanelOpen: false,
          handlePickerState: null,
          publishedSnapshot: null,
          variables: {},
          sessionVariables: {},
          past: [],
          future: [],
        }),
    }),
    { name: 'studio-store' }
  )
)

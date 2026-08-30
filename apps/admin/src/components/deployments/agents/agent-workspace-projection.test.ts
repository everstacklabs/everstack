import { describe, expect, it } from 'vitest'
import yaml from 'js-yaml'
import { TaskPermissionMode } from '@everstack/proto/everstack/agents/v1/agents_pb'
import {
  buildAgentWorkspace,
  buildFileTree,
  syncStateFor,
  PROJECT_META_KEY,
  type ProjectedAgent,
  type ProjectedFunction,
  type WorkspaceFile,
} from './agent-workspace-projection'

function agent(overrides: Partial<ProjectedAgent> = {}): ProjectedAgent {
  return {
    id: 'agt_1',
    name: 'support-bot',
    description: 'Handles support',
    model: 'claude-sonnet-5',
    systemPrompt: '# Role\nBe helpful.',
    tools: [],
    maxTurns: 25,
    maxToolCallsPerTurn: 10,
    taskPermissionMode: TaskPermissionMode.ASK,
    ...overrides,
  }
}

function fnMap(...fns: ProjectedFunction[]): Map<string, ProjectedFunction> {
  return new Map(fns.map((fn) => [fn.name, fn]))
}

function fileAt(files: WorkspaceFile[], path: string): WorkspaceFile {
  const found = files.find((f) => f.path === path)
  if (!found)
    throw new Error(
      `no file at ${path}, have: ${files.map((f) => f.path).join(', ')}`,
    )
  return found
}

function parseYaml(
  files: WorkspaceFile[],
  path: string,
): Record<string, unknown> {
  return yaml.load(fileAt(files, path).content) as Record<string, unknown>
}

describe('buildAgentWorkspace', () => {
  it('uses immutable revision files verbatim, including imported source modules', () => {
    const encoder = new TextEncoder()
    const ws = buildAgentWorkspace({
      agent: agent(),
      revision: {
        id: 'rev-2',
        number: 2,
        digest: 'abcdef0123456789',
        files: [
          {
            path: 'agent.yaml',
            content: encoder.encode('name: support-bot\nmodel: model\n'),
          },
          {
            path: 'src/main.ts',
            content: encoder.encode("import { value } from './value.ts'\n"),
          },
          {
            path: 'src/value.ts',
            content: encoder.encode("export const value = 'ok'\n"),
          },
        ],
      },
      functions: fnMap({
        name: 'stale_global_function',
        runtime: 'deno',
        code: 'must not be projected',
      }),
      triggers: [],
      subagents: [],
    })

    expect(fileAt(ws.files, 'src/main.ts').content).toContain('./value.ts')
    expect(fileAt(ws.files, 'src/value.ts').content).toContain("'ok'")
    expect(
      ws.files.some((file) => file.content === 'must not be projected'),
    ).toBe(false)
    expect(ws.revision).toEqual({
      id: 'rev-2',
      number: 2,
      digest: 'abcdef0123456789',
    })
  })

  it('keeps a linked legacy subagent visible beside a revision-backed root', () => {
    const encoder = new TextEncoder()
    const ws = buildAgentWorkspace({
      agent: agent(),
      revision: {
        id: 'rev-root',
        number: 3,
        digest: 'root-digest',
        files: [
          {
            path: 'agent.yaml',
            content: encoder.encode(
              'name: support-bot\nmodel: model\nsubagents: [./subagents/risk-reviewer]\n',
            ),
          },
        ],
      },
      functions: fnMap(),
      triggers: [],
      subagents: [
        {
          agent: agent({
            id: 'agt_child',
            name: 'risk-reviewer',
            systemPrompt: 'Review risk.',
          }),
          triggers: [],
        },
      ],
    })

    expect(
      fileAt(ws.files, 'subagents/risk-reviewer/instructions.md').content,
    ).toBe('Review risk.\n')
  })

  it('writes instructions, agent.yaml and function-backed tool sources', () => {
    const ws = buildAgentWorkspace({
      agent: agent({ tools: ['web_search', 'get_time', 'score'] }),
      functions: fnMap(
        {
          name: 'get_time',
          runtime: 'deno',
          code: 'export default () => Date.now()',
        },
        {
          name: 'score',
          runtime: 'python3',
          code: 'def handler(a):\n    return a\n',
        },
      ),
      triggers: [],
      subagents: [],
    })

    expect(fileAt(ws.files, 'instructions.md').content).toBe(
      '# Role\nBe helpful.\n',
    )

    // Runtime decides the extension, mirroring pullToolSource.
    expect(fileAt(ws.files, 'tools/get_time.ts').language).toBe('typescript')
    expect(fileAt(ws.files, 'tools/score.py').language).toBe('python')

    const doc = parseYaml(ws.files, 'agent.yaml')
    // Builtin tools stay as bare names; function-backed ones become paths.
    expect(doc.tools).toEqual([
      'web_search',
      './tools/get_time.ts',
      './tools/score.py',
    ])
    expect(doc.permissions).toEqual({ task_mode: 'ask' })
    expect(doc.limits).toMatchObject({
      max_turns: 25,
      max_tool_calls_per_turn: 10,
    })
  })

  it('emits a builtin-only agent with no tools/ files', () => {
    const ws = buildAgentWorkspace({
      agent: agent({ tools: ['web_search'] }),
      functions: fnMap(),
      triggers: [],
      subagents: [],
    })
    expect(ws.files.some((f) => f.path.startsWith('tools/'))).toBe(false)
    expect(parseYaml(ws.files, 'agent.yaml').tools).toEqual(['web_search'])
  })

  it('writes skills to SKILL.md and strips platform-managed config keys', () => {
    const ws = buildAgentWorkspace({
      agent: agent({
        config: {
          temperature: 0.3,
          skills: [
            {
              name: 'billing',
              description: 'Billing playbook',
              content: '# Billing',
            },
          ],
          [PROJECT_META_KEY]: {
            hash: 'abc',
            deployed_at: '2026-07-22T09:00:00Z',
          },
        },
      }),
      functions: fnMap(),
      triggers: [],
      subagents: [],
    })

    expect(fileAt(ws.files, 'skills/billing/SKILL.md').content).toBe(
      '# Billing',
    )

    const doc = parseYaml(ws.files, 'agent.yaml')
    expect(doc.skills).toEqual(['./skills/billing'])
    // skills and the deploy stamp are platform-managed, never re-exported.
    expect(doc.config).toEqual({ temperature: 0.3 })
  })

  it('nests subagents and keeps the subagents key off the nested project', () => {
    const ws = buildAgentWorkspace({
      agent: agent(),
      functions: fnMap({ name: 'score', runtime: 'python3', code: 'x' }),
      triggers: [],
      subagents: [
        {
          agent: agent({
            id: 'agt_2',
            name: 'risk-reviewer',
            model: 'claude-haiku-4-5',
            systemPrompt: 'Assess risk.',
            tools: ['score'],
          }),
          triggers: [],
        },
      ],
    })

    expect(
      fileAt(ws.files, 'subagents/risk-reviewer/instructions.md').content,
    ).toBe('Assess risk.\n')
    expect(
      fileAt(ws.files, 'subagents/risk-reviewer/tools/score.py').content,
    ).toBe('x')

    expect(parseYaml(ws.files, 'agent.yaml').subagents).toEqual([
      './subagents/risk-reviewer',
    ])
    // One level only: the nested project must not declare subagents itself.
    expect(
      parseYaml(ws.files, 'subagents/risk-reviewer/agent.yaml').subagents,
    ).toBeUndefined()
  })

  it('maps triggers into agent.yaml', () => {
    const ws = buildAgentWorkspace({
      agent: agent(),
      functions: fnMap(),
      triggers: [
        {
          triggerType: 'cron',
          name: 'daily',
          cronExpression: '0 9 * * *',
          inputTemplate: 'Digest',
        },
        { triggerType: 'webhook', name: 'inbound' },
      ],
      subagents: [],
    })

    expect(parseYaml(ws.files, 'agent.yaml').triggers).toEqual([
      { type: 'cron', name: 'daily', schedule: '0 9 * * *', input: 'Digest' },
      { type: 'webhook', name: 'inbound' },
    ])
  })
})

describe('syncStateFor', () => {
  const deployedAt = '2026-07-22T09:00:00.000Z'
  const stamped = { [PROJECT_META_KEY]: { hash: 'h', deployed_at: deployedAt } }

  it('reports an agent with no deploy stamp as unmanaged', () => {
    expect(syncStateFor(agent({ config: {} })).kind).toBe('unmanaged')
  })

  it('treats an edit inside the grace window as in sync', () => {
    const state = syncStateFor(
      agent({
        config: stamped,
        updatedAt: new Date('2026-07-22T09:00:03.000Z'),
      }),
    )
    expect(state.kind).toBe('in_sync')
  })

  it('flags an edit after the grace window as drifted', () => {
    const state = syncStateFor(
      agent({
        config: stamped,
        updatedAt: new Date('2026-07-22T09:05:00.000Z'),
      }),
    )
    expect(state.kind).toBe('drifted')
  })

  it('does not claim drift when the stamp is unreadable', () => {
    const state = syncStateFor(
      agent({
        config: { [PROJECT_META_KEY]: { deployed_at: 'not-a-date' } },
        updatedAt: new Date('2026-07-22T09:05:00.000Z'),
      }),
    )
    expect(state.kind).toBe('in_sync')
  })
})

describe('buildFileTree', () => {
  it('nests paths with directories first, then files, each sorted', () => {
    const tree = buildFileTree([
      { path: 'instructions.md', language: 'markdown', content: '' },
      { path: 'agent.yaml', language: 'yaml', content: '' },
      { path: 'tools/get_time.ts', language: 'typescript', content: '' },
      { path: 'skills/billing/SKILL.md', language: 'markdown', content: '' },
    ])

    expect(tree.map((n) => n.name)).toEqual([
      'skills',
      'tools',
      'agent.yaml',
      'instructions.md',
    ])

    const skills = tree.find((n) => n.name === 'skills')
    expect(skills?.children?.[0].name).toBe('billing')
    expect(skills?.children?.[0].children?.[0].path).toBe(
      'skills/billing/SKILL.md',
    )
  })
})

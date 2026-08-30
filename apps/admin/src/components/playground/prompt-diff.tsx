import { DiffEditor } from '@monaco-editor/react'
import yaml from 'js-yaml'
import type { PlaygroundVariant } from '@/stores/playground-store'

/**
 * Serialize a task's prompt (model + sampling params + messages) to YAML, the
 * canonical form the reference UI diffs against. Kept stable/ordered so a diff
 * only highlights real prompt changes, not key reordering.
 */
export function variantToYaml(v: PlaygroundVariant): string {
    const params: Record<string, unknown> = {}
    if (v.temperature !== undefined) params.temperature = v.temperature
    if (v.topP !== undefined) params.top_p = v.topP
    if (v.maxTokens !== undefined) params.max_tokens = v.maxTokens

    return yaml.dump(
        {
            options: {
                model: v.model || null,
                params,
                templating: v.templating,
            },
            prompt: {
                messages: v.messages
                    .filter((m) => m.text.trim())
                    .map((m) => ({ role: m.role, content: m.text })),
            },
        },
        { lineWidth: 80, noRefs: true, sortKeys: false },
    )
}

/**
 * Read-only inline YAML diff of a comparison task against the base task,
 * matching the reference "Diff" toggle. Rendered in place of the message
 * editor while diff mode is on.
 */
export function PromptDiff({ base, variant }: { base: PlaygroundVariant; variant: PlaygroundVariant }) {
    return (
        <DiffEditor
            original={variantToYaml(base)}
            modified={variantToYaml(variant)}
            language="yaml"
            theme="vs-dark"
            height="100%"
            options={{
                readOnly: true,
                renderSideBySide: false,
                minimap: { enabled: false },
                lineNumbers: 'on',
                scrollBeyondLastLine: false,
                fontSize: 11,
                folding: false,
                renderOverviewRuler: false,
                overviewRulerLanes: 0,
                wordWrap: 'on',
                automaticLayout: true,
            }}
        />
    )
}

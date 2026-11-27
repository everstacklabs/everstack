// Session replay records DOM attributes verbatim, even inside masked regions
// (posthog-js has no attribute masking hook). This guard fails when a
// `title`, `aria-label`, `alt` or similar attribute on a DOM element is fed
// by anything other than a string literal, unless it is on the allowlist
// below with a reason. Customer content (commands, prompts, errors, values,
// file names, search terms, URLs, user-chosen names) belongs in visible text
// or a Tooltip, both of which the replay mask covers. See replay-privacy.ts.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import { isStaticExpression, scanSource, type AttributeFinding } from './replay-attribute-scan'

type Allowed = { file: string; attribute: string; expression: string; reason: string }

const PRODUCT_LABEL = 'label from code constants'
const NUMBERS = 'numbers and product text only'
const PROVIDER = 'provider id from the model catalog'
const READINESS = 'product readiness message from taskReadinessError'

const ALLOWED: Allowed[] = [
  { file: 'components/home/model-selector.tsx', attribute: 'alt', expression: 'provider', reason: PROVIDER },
  { file: 'components/providers/model-picker.tsx', attribute: 'alt', expression: 'provider', reason: PROVIDER },
  { file: 'components/gateway/config/load-balancer-form.tsx', attribute: 'alt', expression: 'provider', reason: PROVIDER },
  { file: 'components/providers/provider-icon.tsx', attribute: 'alt', expression: '`${providerName} logo`', reason: PROVIDER },
  { file: 'components/providers/configure-provider-sheet.tsx', attribute: 'title', expression: "model.variants .map((variant) => variant.description) .join('\\n')", reason: 'model catalog descriptions' },
  { file: 'components/traces/trace-sheet-skeleton.tsx', attribute: 'aria-label', expression: 'label', reason: 'loading label; every caller passes a literal' },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: "`Cached: ${cachedTokens} tokens (charged at provider's cache rate)`", reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: "`${cachedTokens > 0 ? 'Fresh input' : 'Input'}: ${totalInput - cachedTokens} tokens`", reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: '`Output: ${totalOutput} tokens`', reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: '`Cached: ${cachedTokens} tokens`', reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: '`Computed: ${totalInput - cachedTokens} tokens`', reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: '`Reasoning: ${reasoningTokens} tokens`', reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: '`Audio: ${audioTokens} tokens`', reason: NUMBERS },
  { file: 'components/traces/trace-overview.tsx', attribute: 'title', expression: '`Text: ${totalOutput - reasoningTokens - audioTokens} tokens`', reason: NUMBERS },
  { file: 'components/playground/run-grid.tsx', attribute: 'title', expression: '`${categoryLabels[config.category]} · ${formatTraceDuration(row.durationNs)}`', reason: NUMBERS },
  { file: 'components/playground/task-test-panel.tsx', attribute: 'title', expression: 'readinessError ?? undefined', reason: READINESS },
  { file: 'components/playground/variant-column.tsx', attribute: 'title', expression: "quickRunError ?? 'Run this variant'", reason: READINESS },
  { file: 'routes/evaluations/playground.tsx', attribute: 'title', expression: "activeVariant ? (taskReadinessError(activeVariant, 'quick') ?? undefined) : undefined", reason: READINESS },
  { file: 'routes/evaluations/playground.tsx', attribute: 'title', expression: "taskReadinessError(activeVariant, 'quick') ?? undefined", reason: READINESS },
  { file: 'components/layout/sidebar/help-menu.tsx', attribute: 'title', expression: 'info?.commit || undefined', reason: 'gateway build commit' },
  { file: 'components/layout/sidebar/user-menu.tsx', attribute: 'aria-label', expression: '`${option.label} theme`', reason: PRODUCT_LABEL },
  { file: 'components/layout/sidebar/user-menu.tsx', attribute: 'title', expression: 'option.label', reason: PRODUCT_LABEL },
  { file: 'components/catalog/catalog-update-banner.tsx', attribute: 'aria-label', expression: '`Dismiss model catalog ${latest.version} notification`', reason: 'catalog version' },
  { file: 'components/brand/everstack-logo.tsx', attribute: 'aria-label', expression: 'label || undefined', reason: PRODUCT_LABEL },
  { file: 'components/observability/metrics/metrics-toolbar.tsx', attribute: 'title', expression: '`Last updated ${lastUpdated}`', reason: 'timestamp' },
  { file: 'components/logs/logs-console.tsx', attribute: 'title', expression: 'def.label', reason: PRODUCT_LABEL },
  { file: 'components/onboarding/onboarding-checklist.tsx', attribute: 'title', expression: '`Copy ${label} URL`', reason: PRODUCT_LABEL },
  { file: 'components/gateway/config/deployment-time-badge.tsx', attribute: 'title', expression: 'reason', reason: 'product explanation; default and callers are literals' },
  { file: 'components/deployments/studio/chat/chat-panel.tsx', attribute: 'title', expression: 'TAB_LABELS[t]', reason: PRODUCT_LABEL },
  { file: 'components/deployments/studio/chat/browser-run-panel.tsx', attribute: 'alt', expression: "`Browser snapshot ${selectedSnapshot?.sequence ?? ''}`", reason: 'sequence number' },
  { file: 'components/deployments/studio/canvas/canvas-controls.tsx', attribute: 'title', expression: 'title', reason: 'control label; callers pass literals' },
  { file: 'components/deployments/agents/mcp-tab-content.tsx', attribute: 'title', expression: 'label', reason: 'health status label' },
  { file: 'components/deployments/agents/session-timeline.tsx', attribute: 'aria-label', expression: '`Context window ${pct.toFixed(0)}% used`', reason: NUMBERS },
  { file: 'components/deployments/agents/agent-form.tsx', attribute: 'title', expression: 'c.label', reason: 'colour name from AGENT_COLORS' },
  { file: 'components/deployments/troopers/trooper-form-sheet.tsx', attribute: 'title', expression: 'c.label', reason: 'colour name from a constant palette' },
  { file: 'components/deployments/agents/deployment-detail-sheet.tsx', attribute: 'title', expression: 'label', reason: 'copy button label; callers pass literals' },
  { file: 'components/deployments/agents/chat-presentation.tsx', attribute: 'aria-label', expression: 'placeholder', reason: 'composer placeholder; callers pass literals' },
  { file: 'components/deployments/sandbox/processes-tab.tsx', attribute: 'aria-label', expression: '`Delete session ${id}`', reason: 'generated session id' },
  { file: 'components/deployments/sandbox/volumes-tab.tsx', attribute: 'title', expression: 'vol.id', reason: 'generated volume id' },
  { file: 'components/deployments/sandbox/shell-tab-strip.tsx', attribute: 'title', expression: '`${label} · ${row.id}`', reason: '"Shell N" and generated session id' },
  { file: 'components/deployments/sandbox/shell-tab-strip.tsx', attribute: 'title', expression: '`${row.attached_clients} attached`', reason: NUMBERS },
  { file: 'components/deployments/sandbox/ports-tab.tsx', attribute: 'title', expression: '`Expose port ${dp.port}`', reason: 'port number' },
  { file: 'routes/evaluations/runs_.$runId.tsx', attribute: 'title', expression: '`Open trace ${tid}`', reason: 'generated trace id' },
  { file: 'routes/evaluations/runs_.compare.tsx', attribute: 'title', expression: '`95% CI [${s.ciLow.toFixed(3)}, ${s.ciHigh.toFixed(3)}]`', reason: NUMBERS },
  { file: 'routes/evaluations/runs_.compare.tsx', attribute: 'title', expression: '`${d.name}: ${fmtMean(d.baseline)} → ${fmtMean(d.candidate)}`', reason: 'scorer name and numbers' },
  { file: 'routes/observability/saved-queries.tsx', attribute: 'aria-label', expression: 'label', reason: 'icon button label; callers pass literals' },
  { file: 'routes/observability/saved-queries.tsx', attribute: 'title', expression: 'label', reason: 'icon button label; callers pass literals' },
  // Links: same-origin paths, ids and product URLs only. Anything built from
  // customer data is wrapped in replayBlock instead.
  { file: 'components/settings/usage/upgrade-license-dialog.tsx', attribute: 'href', expression: "import.meta.env.VITE_CLOUD_URL || 'https://app.everstack.ai'", reason: 'build-time Cloud URL' },
  { file: 'components/evaluations/create-eval-run-page.tsx', attribute: 'href', expression: 'EVAL_RUNS_DOCS_URL', reason: 'docs URL constant' },
  { file: 'components/ee/feature-gate-banner.tsx', attribute: 'href', expression: 'upgradeUrl', reason: 'plan upgrade URL from the feature gate response' },
  { file: 'components/layout/sidebar/help-menu.tsx', attribute: 'href', expression: 'link.href', reason: 'help link constants' },
  { file: 'components/layout/sidebar/sidebar-nav.tsx', attribute: 'href', expression: 'learnMoreHref', reason: 'docs URL constants' },
  { file: 'components/layout/chat-first-layout.tsx', attribute: 'href', expression: '`/deployments/agents/${agentId}/overview`', reason: 'same-origin path with a generated id' },
  { file: 'routes/evaluations/runs_.$runId.tsx', attribute: 'href', expression: '`/observability/traces?trace=${encodeURIComponent(tid)}`', reason: 'same-origin path; the query is a generated trace id (and the block selector hides it anyway)' },
  { file: 'routes/evaluations/datasets_.$datasetId.tsx', attribute: 'href', expression: 'selectedExample.docsUrl', reason: 'docs URL constants' },
  { file: 'routes/evaluations/runs.tsx', attribute: 'href', expression: 'selectedExample.docsUrl', reason: 'docs URL constants' },
  { file: 'routes/observability/issues_.$issueId.tsx', attribute: 'href', expression: '`#sec-${a.toLowerCase()}`', reason: 'in-page section anchor from constants' },
]

const SRC = join(__dirname, '..')

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return name.endsWith('.tsx') && !name.endsWith('.test.tsx') ? [path] : []
  })
}

const key = (f: { file: string; attribute: string; expression: string }) => `${f.file} ${f.attribute}={${f.expression}}`

describe('recorded DOM attributes carry no customer content', () => {
  const findings: AttributeFinding[] = sourceFiles(SRC).flatMap((path) =>
    scanSource(relative(SRC, path), readFileSync(path, 'utf8')),
  )
  const allowed = new Set(ALLOWED.map(key))

  it('every dynamic title/aria-label/alt on a DOM element is reviewed', () => {
    const unreviewed = findings.filter((f) => !allowed.has(key(f))).map((f) => `${f.file}:${f.line} ${f.attribute}={${f.expression}}`)
    expect(
      unreviewed,
      'Move customer content into visible text or a Tooltip, or add a reviewed allowlist entry',
    ).toEqual([])
  })

  it('has no stale allowlist entries', () => {
    const present = new Set(findings.map(key))
    expect(ALLOWED.filter((a) => !present.has(key(a))).map(key)).toEqual([])
  })

  it('still flags the attributes the privacy review found', () => {
    const scan = (code: string) => scanSource('x.tsx', code)
    expect(scan('<td title={cron.command}>{cron.command}</td>')).toHaveLength(1)
    expect(scan('<span aria-label={seg.value} />')).toHaveLength(1)
    expect(scan('<Button title={nodeEvt.error} />')).toHaveLength(1)
    expect(scan('<div title={copied ? "Copied" : "Copy"} aria-label="Copy" />')).toHaveLength(0)
    // A component prop rendered as visible text is covered by the text mask.
    expect(scan('<SectionHeader title={view.name} />')).toHaveLength(0)
    // Links built from data must be blocked in replay or reviewed.
    expect(scan('<a href={p.url}>open</a>')).toHaveLength(1)
    expect(scan('<a {...replayBlock} href={p.url}>open</a>')).toHaveLength(0)
    expect(scan('<a data-ph-block="" href={p.url}>open</a>')).toHaveLength(0)
    expect(scan('<a href="/docs">docs</a>')).toHaveLength(0)
  })

  it('treats only literal-valued expressions as static', () => {
    const expr = (code: string) => {
      const sf = ts.createSourceFile('x.ts', `(${code})`, ts.ScriptTarget.Latest, true)
      return (sf.statements[0] as ts.ExpressionStatement).expression
    }
    expect(isStaticExpression(expr("a ? 'x' : undefined"))).toBe(true)
    expect(isStaticExpression(expr('`Remove ${tag}`'))).toBe(false)
    expect(isStaticExpression(expr("name || 'Untitled'"))).toBe(false)
  })
})

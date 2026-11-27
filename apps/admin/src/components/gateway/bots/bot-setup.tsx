import { Fragment, useMemo, useState, type ReactNode } from 'react'
import { ui } from '@everstack/ui'
import { CodeBox, CopyField } from '@/components/gateway/interop/copy-field'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { useAgents } from '@/hooks/deployments/use-agents'
import { useA2aPublished } from '@/hooks/gateway/use-interop'
import { getApiBaseUrl } from '@/lib/api-url'
import { renderSetup, type RenderedBlock } from './bot-catalog'

const { Input, Label, Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } = ui

export const inputClass = 'bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50'
export const selectTriggerClass =
    'w-full bg-brand-main-900/60 border-brand-main-600 text-white light:text-brand-main-50'
export const selectContentClass = 'bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50'
export const mutedClass = 'text-white/45 light:text-black/45'

/** Renders `code` spans in catalog prose. */
export function Prose({ text }: { text: string }) {
    const parts = text.split('`')
    return (
        <>
            {parts.map((part, i) =>
                i % 2 === 1 ? (
                    <code
                        key={i}
                        className="rounded bg-brand-main-800/60 px-1 py-px font-mono text-[11px] text-brand-secondary-200"
                    >
                        {part}
                    </code>
                ) : (
                    <Fragment key={i}>{part}</Fragment>
                ),
            )}
        </>
    )
}

function Caveats({ items }: { items: string[] }) {
    if (items.length === 0) return null
    return (
        <ul className="space-y-1 text-xs leading-5 text-brand-main-200">
            {items.map((item) => (
                <li key={item} className="flex gap-2">
                    <span aria-hidden className="mt-2 size-1 shrink-0 rounded-full bg-brand-main-400" />
                    <span>
                        <Prose text={item} />
                    </span>
                </li>
            ))}
        </ul>
    )
}

// The code block highlighter has no JSON5 grammar; JavaScript reads the same.
const highlightAs = (language: string) => (language === 'json5' ? 'javascript' : language)

function BlockView({ block, onCopyText }: { block: RenderedBlock; onCopyText: (text: string) => (() => void) | undefined }) {
    return (
        <div className="min-w-0 space-y-2">
            <h4 className="text-xs font-medium text-brand-main-200">{block.title}</h4>
            {block.type === 'code' && (
                // The sheet body's auto-hiding scrollbar colour is inherited, which would
                // give long lines a thick bright bar; keep theirs thin and quiet.
                <div className="min-w-0 max-w-full [scrollbar-color:var(--color-brand-main-600)_transparent] [&_*]:[scrollbar-width:thin] [&_pre]:overflow-x-auto">
                    <CodeBox code={block.code} language={highlightAs(block.language)} onCopy={onCopyText(block.code)} />
                </div>
            )}
            {block.type === 'values' && (
                <>
                    <p className="rounded border border-warning-border bg-warning-surface px-3 py-2 text-xs leading-5 text-warning">
                        {block.intro}
                    </p>
                    <dl className="space-y-2">
                        {block.values.map((v) => (
                            <div key={v.label} className="grid gap-1 sm:grid-cols-[120px_1fr] sm:items-center sm:gap-3">
                                <dt className="text-[11px] text-brand-main-300">{v.label}</dt>
                                <dd className="min-w-0">
                                    <CopyField value={v.value} onCopy={onCopyText(v.value)} ariaLabel={`Copy ${v.label}`} wrap />
                                </dd>
                            </div>
                        ))}
                    </dl>
                </>
            )}
            {block.type === 'steps' && (
                <ol className="list-decimal space-y-1 pl-5 text-xs leading-5 text-brand-main-100">
                    {block.steps.map((step) => (
                        <li key={step}>
                            <Prose text={step} />
                        </li>
                    ))}
                </ol>
            )}
            <Caveats items={block.caveats} />
        </div>
    )
}

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: ReactNode }) {
    return (
        <div className="min-w-0 space-y-1.5">
            <Label htmlFor={id} className="text-xs">
                {label}
            </Label>
            {children}
            {hint && <p className={`text-[11px] leading-4 ${mutedClass}`}>{hint}</p>}
        </div>
    )
}

function ModelField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
    const { data: groups = [] } = useGatewayModels()
    const hasModels = groups.some((g) => g.models.length > 0)
    if (!hasModels) {
        return (
            <Field id="bot-setup-model" label="Model" hint="Use a model id from the Models page.">
                <Input
                    id="bot-setup-model"
                    value={value}
                    onChange={(e) => onChange(e.target.value)}
                    placeholder="<model>"
                    className={inputClass}
                />
            </Field>
        )
    }
    return (
        <Field id="bot-setup-model" label="Model" hint="Fills the model id into the config.">
            <Select value={value} onValueChange={(v) => v && onChange(v)}>
                <SelectTrigger id="bot-setup-model" className={selectTriggerClass}>
                    <SelectValue placeholder="Choose a model" />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                    {groups
                        .filter((g) => g.models.length > 0)
                        .map((g) => (
                            <SelectGroup key={g.provider}>
                                <SelectLabel>{g.provider}</SelectLabel>
                                {g.models.map((m) => (
                                    <SelectItem key={`${g.provider}/${m}`} value={m}>
                                        {m}
                                    </SelectItem>
                                ))}
                            </SelectGroup>
                        ))}
                </SelectContent>
            </Select>
        </Field>
    )
}

function AgentField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
    const { data: agents = [] } = useAgents()
    const { data: published = {} } = useA2aPublished()
    const options = agents.filter((a) => published[a.id])
    if (options.length === 0) {
        return (
            <Field id="bot-setup-agent" label="Agent ID" hint="Publish an agent under Gateway, A2A to call it.">
                <Input
                    id="bot-setup-agent"
                    value={value}
                    onChange={(e) => onChange(e.target.value)}
                    placeholder="<agent id>"
                    className={inputClass}
                />
            </Field>
        )
    }
    return (
        <Field id="bot-setup-agent" label="Agent" hint="One of your agents published over A2A.">
            <Select value={value} onValueChange={(v) => v && onChange(v)}>
                <SelectTrigger id="bot-setup-agent" className={selectTriggerClass}>
                    <SelectValue placeholder="Choose an agent" />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                    {options.map((a) => (
                        <SelectItem key={a.id} value={a.id}>
                            {a.name}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </Field>
    )
}

/** One part of the setup, set off from the part above it by a rule. */
function SetupSection({ label, ariaLabel, children }: { label: string; ariaLabel?: string; children: ReactNode }) {
    return (
        <section className="min-w-0 space-y-3 border-t border-brand-main-600/40 pt-5" aria-label={ariaLabel ?? label}>
            <h3 className="text-sm font-medium text-brand-secondary-100">{label}</h3>
            {children}
        </section>
    )
}

/** Where this bot's telemetry ends up. Copying the id is not copying the key,
 *  so it never counts toward the close check. */
function KeyIdAttribute({ apiKeyId }: { apiKeyId: string }) {
    return (
        <div className="min-w-0 space-y-1.5">
            <p className="text-xs leading-5 text-brand-main-200">
                <Prose text="Traces, logs and metrics sent with this key carry the resource attribute `everstack.api_key_id` with this value:" />
            </p>
            <CopyField value={apiKeyId} ariaLabel="Copy everstack.api_key_id value" wrap />
        </div>
    )
}

/** The config a bot needs, filled in with this gateway's URL and, right after
 *  the key is issued, the key itself. Without a key it shows a placeholder. */
export function BotSetup({
    kind,
    surfaces,
    apiKey,
    apiKeyId,
    onCopy,
}: {
    kind: string
    surfaces: string[]
    apiKey?: string
    /** Shown in the Traces section so the bot's telemetry can be found. */
    apiKeyId?: string
    onCopy?: () => void
}) {
    const [model, setModel] = useState('')
    const [agentId, setAgentId] = useState('')
    const gatewayUrl = getApiBaseUrl()
    const setup = useMemo(
        () => renderSetup(kind, surfaces, { gatewayUrl, apiKey, model, agentId }),
        [kind, surfaces, gatewayUrl, apiKey, model, agentId],
    )
    if (!setup) {
        return <p className={`text-sm ${mutedClass}`}>There are no setup steps for this bot type.</p>
    }
    const { kind: entry, merged, sections } = setup
    if (sections.length === 0) {
        return <p className={`text-sm ${mutedClass}`}>This bot has no surfaces enabled. Edit it to choose some.</p>
    }
    // Only a copy that carries the key counts as having saved it.
    const onCopyText = (text: string) => (apiKey && onCopy && text.includes(apiKey) ? onCopy : undefined)
    const usesModel = surfaces.includes('inference')
    const usesAgent = surfaces.includes('a2a')

    return (
        <div className="min-w-0 space-y-6">
            <dl className="divide-y divide-brand-main-600/40 rounded border border-brand-main-600/40 text-xs leading-5">
                <div className="grid gap-x-3 gap-y-0.5 px-3 py-2.5 sm:grid-cols-[72px_1fr]">
                    <dt className="text-brand-main-300">Settings</dt>
                    <dd className="min-w-0 text-brand-main-100">
                        <Prose text={entry.configLocation} />.
                    </dd>
                </div>
                <div className="grid gap-x-3 gap-y-0.5 px-3 py-2.5 sm:grid-cols-[72px_1fr]">
                    <dt className="text-brand-main-300">Apply</dt>
                    <dd className="min-w-0 text-brand-main-100">
                        <Prose text={entry.apply} />
                    </dd>
                </div>
            </dl>
            <div className="grid gap-4 sm:grid-cols-2">
                <div className="min-w-0 space-y-1.5 sm:col-span-2">
                    <span className="text-xs font-medium">Gateway URL</span>
                    <CopyField value={gatewayUrl} ariaLabel="Copy gateway URL" />
                </div>
                {usesModel && <ModelField value={model} onChange={setModel} />}
                {usesAgent && <AgentField value={agentId} onChange={setAgentId} />}
            </div>
            {merged && (
                <SetupSection label="Everything in one file" ariaLabel="Single file">
                    {merged.note && <p className="text-xs leading-5 text-brand-main-200">{merged.note}</p>}
                    <BlockView block={merged} onCopyText={onCopyText} />
                </SetupSection>
            )}
            {sections.map((section) =>
                section.blocks.length === 0 && section.notes.length === 0 && !(section.surface === 'telemetry' && apiKeyId) ? null : (
                    <SetupSection key={section.surface} label={section.label}>
                        <Caveats items={section.notes} />
                        {section.surface === 'telemetry' && apiKeyId && <KeyIdAttribute apiKeyId={apiKeyId} />}
                        {section.blocks.map((block) => (
                            <BlockView key={block.title} block={block} onCopyText={onCopyText} />
                        ))}
                    </SetupSection>
                ),
            )}
        </div>
    )
}

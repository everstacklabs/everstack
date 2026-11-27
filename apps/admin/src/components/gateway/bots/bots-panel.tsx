import { useState } from 'react'
import { ui } from '@everstack/ui'
import {
    describeBotError,
    isSessionError,
    useBots,
    useDeleteBot,
    useRotateBotKey,
    useUpdateBot,
    type Bot,
    type UpdateBotInput,
} from '@/hooks/gateway/use-bots'
import { useRemoteAgents } from '@/hooks/gateway/use-interop'
import { SURFACE_INFO, SURFACES, kindEntry, kindName, type Surface } from './bot-catalog'
import { BotLogo } from './bot-logo'
import { BotSetup, inputClass, mutedClass } from './bot-setup'
import { BotSheet, BotSheetActions } from './bot-sheet'
import { ConnectBotSheet } from './connect-bot-sheet'
import { KeyReveal, useGuardedClose } from './key-reveal'
import { RateLimitField, RemoteSelect, SurfaceChecklist, parseRateLimitInput } from './bot-fields'

const {
    AlertDialog,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    Button,
    Input,
    Label,
} = ui

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
    ['year', 365 * 24 * 3600],
    ['month', 30 * 24 * 3600],
    ['week', 7 * 24 * 3600],
    ['day', 24 * 3600],
    ['hour', 3600],
    ['minute', 60],
]

// The gateway masks a key as its first and last characters around a long run
// of asterisks. A row only needs enough to tell two keys apart.
export function shortKeyHint(hint: string): string {
    if (hint.length <= 14) return hint
    return `${hint.slice(0, 3)}...${hint.slice(-4)}`
}

export function relativeTime(iso: string, now = Date.now()): string {
    const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
    if (Number.isNaN(seconds)) return ''
    const format = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
    for (const [unit, size] of UNITS) {
        if (Math.abs(seconds) >= size) return format.format(Math.trunc(seconds / size), unit)
    }
    return 'just now'
}

const count = (n: number) => n.toLocaleString('en-US')

/** One quiet line of 30-day usage. Hidden for a bot that never called. The
 *  per-surface split is in the title. */
export function UsageLine({ bot }: { bot: Bot }) {
    const calls = bot.usage?.calls_30d ?? 0
    if (calls === 0 && !bot.last_seen_at) return null
    const bySurface = bot.usage?.by_surface ?? {}
    const breakdown = SURFACES.filter((s) => (bySurface[s] ?? 0) > 0)
        .map((s) => `${SURFACE_INFO[s].label} ${count(bySurface[s] ?? 0)}`)
        .join(', ')
    return (
        <span title={calls > 0 && breakdown ? breakdown : undefined}>
            {count(calls)} {calls === 1 ? 'call' : 'calls'} in 30 days
            {calls > 0 && breakdown && <span className="sr-only"> ({breakdown})</span>}
        </span>
    )
}

function SurfaceChips({ surfaces }: { surfaces: string[] }) {
    return (
        <span className="flex flex-wrap gap-1">
            {SURFACES.filter((s) => surfaces.includes(s)).map((s: Surface) => (
                <span
                    key={s}
                    className="rounded border border-brand-secondary-500/30 bg-brand-secondary-600/15 px-1.5 py-px text-[10px] text-brand-secondary-200"
                >
                    {SURFACE_INFO[s].label}
                </span>
            ))}
        </span>
    )
}

function ConfirmDialog({
    open,
    title,
    description,
    confirmLabel,
    pending,
    error,
    onConfirm,
    onCancel,
}: {
    open: boolean
    title: string
    description: string
    confirmLabel: string
    pending: boolean
    error: unknown
    onConfirm: () => void
    onCancel: () => void
}) {
    return (
        <AlertDialog open={open} onOpenChange={(value) => !value && !pending && onCancel()}>
            <AlertDialogContent className="border border-brand-main-700 bg-brand-main-900 text-brand-main-100 sm:max-w-md">
                <AlertDialogHeader>
                    <AlertDialogTitle className="text-white light:text-brand-main-50">{title}</AlertDialogTitle>
                    <AlertDialogDescription className="text-white/55 light:text-black/55">{description}</AlertDialogDescription>
                </AlertDialogHeader>
                {error ? (
                    <p role="alert" className="text-sm text-danger">
                        {describeBotError(error)}
                    </p>
                ) : null}
                <AlertDialogFooter>
                    <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
                    <Button variant="destructive" disabled={pending} onClick={onConfirm}>
                        {pending ? 'Working…' : confirmLabel}
                    </Button>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    )
}

// Mounted fresh for each open, so it starts from that bot's values.
function EditBotSheet({ bot, onClose }: { bot: Bot | null; onClose: () => void }) {
    const update = useUpdateBot()
    const remotes = useRemoteAgents()
    const [name, setName] = useState(bot?.name ?? '')
    const [surfaces, setSurfaces] = useState<string[]>(bot?.surfaces ?? [])
    const [remote, setRemote] = useState(bot?.remote_agent ?? '')
    const [rateLimit, setRateLimit] = useState(bot?.rate_limit_rpm != null ? String(bot.rate_limit_rpm) : '')
    const limit = parseRateLimitInput(rateLimit)

    const remoteList = remotes.data ?? []
    const showRemote = Boolean(kindEntry(bot?.kind ?? '')?.exposesA2a) && (remoteList.length > 0 || Boolean(bot?.remote_agent))
    const save = () => {
        if (!bot || !limit.ok) return
        const body: UpdateBotInput = { id: bot.id }
        if (name.trim() !== bot.name) body.name = name.trim()
        const sameSurfaces =
            surfaces.length === bot.surfaces.length && surfaces.every((s) => bot.surfaces.includes(s))
        if (!sameSurfaces) body.surfaces = surfaces
        if ((remote || null) !== bot.remote_agent) body.remote_agent = remote || null
        if (limit.value !== (bot.rate_limit_rpm ?? null)) body.rate_limit_rpm = limit.value
        if (Object.keys(body).length === 1) {
            onClose()
            return
        }
        update.mutate(body, { onSuccess: onClose })
    }

    const formId = 'edit-bot-form'

    return (
        <BotSheet
            open={bot !== null}
            // Closing mid-save would hide whether the change went through.
            onOpenChange={(value) => !value && !update.isPending && onClose()}
            kind={bot?.kind}
            title="Edit bot"
            description={bot ? `${kindName(bot.kind)}. The key stays the same.` : ''}
            footer={
                <BotSheetActions error={update.isError ? `Could not save. ${describeBotError(update.error)}` : undefined}>
                    <Button type="button" variant="outline" onClick={onClose} disabled={update.isPending}>
                        Cancel
                    </Button>
                    <Button
                        type="submit"
                        form={formId}
                        disabled={update.isPending || !name.trim() || surfaces.length === 0 || !limit.ok}
                    >
                        {update.isPending ? 'Saving…' : 'Save'}
                    </Button>
                </BotSheetActions>
            }
        >
            <form
                id={formId}
                className="min-w-0 space-y-6"
                onSubmit={(event) => {
                    event.preventDefault()
                    save()
                }}
            >
                <div className="space-y-2">
                    <Label htmlFor="edit-bot-name">Name</Label>
                    <Input
                        id="edit-bot-name"
                        value={name}
                        onChange={(event) => setName(event.target.value)}
                        maxLength={64}
                        required
                        className={inputClass}
                    />
                </div>
                {bot && <SurfaceChecklist kind={bot.kind} value={surfaces} onChange={setSurfaces} idPrefix="edit" />}
                {showRemote && (
                    <RemoteSelect
                        remotes={
                            bot?.remote_agent && !remoteList.some((r) => r.name === bot.remote_agent)
                                ? [...remoteList, { id: bot.remote_agent, name: bot.remote_agent } as (typeof remoteList)[number]]
                                : remoteList
                        }
                        value={remote}
                        onChange={setRemote}
                    />
                )}
                <RateLimitField value={rateLimit} onChange={setRateLimit} idPrefix="edit" />
            </form>
        </BotSheet>
    )
}

/** Setup for an existing bot. Right after a rotation it also holds the new
 *  key; otherwise the snippets show a placeholder. */
function SetupSheet({ target, onClose }: { target: { bot: Bot; key?: string } | null; onClose: () => void }) {
    const guard = useGuardedClose(() => {
        guard.reset()
        onClose()
    })
    const hasKey = Boolean(target?.key)
    return (
        <>
            <BotSheet
                open={target !== null}
                onOpenChange={(value) => !value && guard.request(hasKey)}
                kind={target?.bot.kind}
                title={target ? (hasKey ? `New key for ${target.bot.name}` : `Set up ${target.bot.name}`) : ''}
                description={
                    target
                        ? hasKey
                            ? 'The previous key stops working within 30 seconds. Update the bot with this one.'
                            : `For ${kindName(target.bot.kind)}. Keys are shown only once, so snippets use a placeholder for it.`
                        : ''
                }
                footer={
                    <BotSheetActions>
                        <Button onClick={() => guard.request(hasKey)}>Done</Button>
                    </BotSheetActions>
                }
            >
                {target &&
                    (target.key ? (
                        <KeyReveal
                            apiKey={target.key}
                            apiKeyId={target.bot.api_key_id}
                            kind={target.bot.kind}
                            surfaces={target.bot.surfaces}
                            onCopied={guard.markCopied}
                        />
                    ) : (
                        <BotSetup kind={target.bot.kind} surfaces={target.bot.surfaces} apiKeyId={target.bot.api_key_id} />
                    ))}
            </BotSheet>
            {guard.confirm}
        </>
    )
}

export function BotsPanel() {
    const bots = useBots()
    const remotes = useRemoteAgents()
    const rotate = useRotateBotKey()
    const del = useDeleteBot()
    const [connectOpen, setConnectOpen] = useState(false)
    const [editing, setEditing] = useState<Bot | null>(null)
    // Each Edit click mounts a fresh sheet; closing keeps that one mounted so it
    // can slide shut.
    const [editSeq, setEditSeq] = useState(0)
    const [setup, setSetup] = useState<{ bot: Bot; key?: string } | null>(null)
    const [rotating, setRotating] = useState<Bot | null>(null)
    const [removing, setRemoving] = useState<Bot | null>(null)

    const list = bots.data ?? []
    const sessionLost = bots.isError && isSessionError(bots.error)

    const confirmRotate = () => {
        if (!rotating) return
        let key = ''
        rotate.mutate(
            {
                id: rotating.id,
                onKey: (value) => {
                    key = value
                },
            },
            {
                onSuccess: (bot) => {
                    setRotating(null)
                    rotate.reset()
                    setSetup({ bot, key })
                },
            },
        )
    }

    return (
        <div className="space-y-6 p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                    <h2 className="text-sm font-medium text-brand-secondary-100">Personal bots</h2>
                    <p className="mt-1 max-w-xl text-xs leading-5 text-brand-main-300">
                        Each bot gets its own key that can only use the parts of this gateway you allow. The bot runs
                        wherever you run it.
                    </p>
                </div>
                {list.length > 0 && <Button onClick={() => setConnectOpen(true)}>Connect a bot</Button>}
            </div>

            {bots.isLoading && <p className={`text-sm ${mutedClass}`}>Loading bots…</p>}
            {sessionLost && (
                <p role="alert" className="text-sm text-danger">
                    {describeBotError(bots.error)}{' '}
                    <a className="underline" href="/login">
                        Sign in
                    </a>
                </p>
            )}
            {bots.isError && !sessionLost && (
                <p role="alert" className="text-sm text-danger">
                    Unable to load bots. {describeBotError(bots.error)}{' '}
                    <button type="button" className="underline" onClick={() => bots.refetch()}>
                        Retry
                    </button>
                </p>
            )}

            {bots.isSuccess && list.length === 0 && (
                <div className="flex flex-col items-start gap-3 rounded border border-dashed border-brand-main-600 bg-brand-main-900/40 p-4">
                    <p className="max-w-xl text-sm leading-6 text-brand-main-100">
                        A bot connection gives one personal bot, such as OpenClaw or Hermes Agent, its own key that can
                        only use the models, tools, traces or agents you choose. After you connect it, you get the exact
                        config to paste into the bot.
                    </p>
                    <Button onClick={() => setConnectOpen(true)}>Connect a bot</Button>
                </div>
            )}

            {list.length > 0 && (
                <ul className="space-y-2" aria-label="Bots">
                    {list.map((bot) => (
                        <li
                            key={bot.id}
                            className="flex flex-col gap-2 rounded border border-brand-main-600/40 bg-brand-main-800/40 px-3 py-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3"
                        >
                            <div className="flex min-w-0 flex-col gap-1 sm:flex-1">
                                <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                                    <BotLogo kind={bot.kind} />
                                    <span className="truncate text-sm text-brand-secondary-100">{bot.name}</span>
                                    <span className="text-[11px] text-brand-main-300">{kindName(bot.kind)}</span>
                                    <SurfaceChips surfaces={bot.surfaces} />
                                </span>
                                <span className="flex flex-wrap gap-x-3 text-[11px] text-brand-main-300">
                                    <span>
                                        Key <code className="font-mono">{shortKeyHint(bot.key_hint)}</code>
                                    </span>
                                    <span>
                                        {bot.last_seen_at
                                            ? `Last seen ${relativeTime(bot.last_seen_at)}`
                                            : 'Not connected yet'}
                                    </span>
                                    <UsageLine bot={bot} />
                                    {bot.rate_limit_rpm != null && <span>{count(bot.rate_limit_rpm)} requests/min</span>}
                                    {bot.remote_agent && <span>Remote agent: {bot.remote_agent}</span>}
                                </span>
                            </div>
                            <div className="flex shrink-0 flex-wrap gap-1">
                                <Button variant="ghost" size="sm" onClick={() => setSetup({ bot })} aria-label={`Setup ${bot.name}`}>
                                    Setup
                                </Button>
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => {
                                        setEditing(bot)
                                        setEditSeq((n) => n + 1)
                                    }}
                                    aria-label={`Edit ${bot.name}`}
                                >
                                    Edit
                                </Button>
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => {
                                        rotate.reset()
                                        setRotating(bot)
                                    }}
                                    aria-label={`New key for ${bot.name}`}
                                >
                                    New key
                                </Button>
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => {
                                        del.reset()
                                        setRemoving(bot)
                                    }}
                                    aria-label={`Remove ${bot.name}`}
                                >
                                    Remove
                                </Button>
                            </div>
                        </li>
                    ))}
                </ul>
            )}

            <ConnectBotSheet
                open={connectOpen}
                onOpenChange={setConnectOpen}
                bots={list}
                remotes={remotes.data ?? []}
            />
            <EditBotSheet key={editSeq} bot={editing} onClose={() => setEditing(null)} />
            <SetupSheet target={setup} onClose={() => setSetup(null)} />
            <ConfirmDialog
                open={rotating !== null}
                title={`Make a new key for ${rotating?.name ?? ''}?`}
                description="The current key stops working within 30 seconds. You will see the new key once and need to update the bot with it. Its 30-day call count starts again from zero."
                confirmLabel="Make new key"
                pending={rotate.isPending}
                error={rotate.error}
                onConfirm={confirmRotate}
                onCancel={() => setRotating(null)}
            />
            <ConfirmDialog
                open={removing !== null}
                title={`Remove ${removing?.name ?? ''}?`}
                description="The bot's key is revoked and stops working within 30 seconds. The bot itself keeps running wherever you host it."
                confirmLabel="Remove"
                pending={del.isPending}
                error={del.error}
                onConfirm={() =>
                    removing && del.mutate({ id: removing.id }, { onSuccess: () => setRemoving(null) })
                }
                onCancel={() => setRemoving(null)}
            />
        </div>
    )
}

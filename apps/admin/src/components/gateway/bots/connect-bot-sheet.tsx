import { useEffect, useRef, useState } from 'react'
import { ui } from '@everstack/ui'
import { describeBotError, useCreateBot, type Bot } from '@/hooks/gateway/use-bots'
import type { RemoteAgent } from '@/hooks/gateway/use-interop'
import { BOT_CATALOG, defaultSurfaces, kindEntry, type BotKind } from './bot-catalog'
import { BotLogo } from './bot-logo'
import { BotSheet, BotSheetActions } from './bot-sheet'
import { inputClass } from './bot-setup'
import { KeyReveal, useGuardedClose } from './key-reveal'
import { RateLimitField, RemoteSelect, SurfaceChecklist, parseRateLimitInput } from './bot-fields'

const { Badge, Button, Input, Label } = ui

function suggestName(kind: string, bots: Bot[]): string {
    const taken = new Set(bots.map((b) => b.name))
    for (let n = 1; ; n += 1) {
        const name = `${kind}-${n}`
        if (!taken.has(name)) return name
    }
}

export function ConnectBotSheet({
    open,
    onOpenChange,
    bots,
    remotes,
}: {
    open: boolean
    onOpenChange: (open: boolean) => void
    bots: Bot[]
    remotes: RemoteAgent[]
}) {
    const create = useCreateBot()
    const [kind, setKind] = useState<BotKind>('openclaw')
    const [name, setName] = useState('')
    const [nameEdited, setNameEdited] = useState(false)
    const [surfaces, setSurfaces] = useState<string[]>(defaultSurfaces('openclaw'))
    const [remote, setRemote] = useState('')
    const [rateLimit, setRateLimit] = useState('')
    // The key lives only here, for as long as this panel is open.
    const [created, setCreated] = useState<{ bot: Bot; key: string } | null>(null)
    // Step 2 starts at the top, where the key is, even if step 1 was scrolled.
    const bodyRef = useRef<HTMLDivElement>(null)
    useEffect(() => {
        if (created && bodyRef.current) bodyRef.current.scrollTop = 0
    }, [created])

    const close = () => {
        setCreated(null)
        create.reset()
        guard.reset()
        onOpenChange(false)
    }
    const guard = useGuardedClose(close)

    const chooseKind = (next: BotKind) => {
        setKind(next)
        setSurfaces(defaultSurfaces(next))
        if (!kindEntry(next)?.exposesA2a) setRemote('')
        if (!nameEdited) setName(suggestName(next, bots))
    }

    // Reset the form each time the panel opens.
    const [wasOpen, setWasOpen] = useState(false)
    if (open !== wasOpen) {
        setWasOpen(open)
        if (open) {
            setKind('openclaw')
            setSurfaces(defaultSurfaces('openclaw'))
            setName(suggestName('openclaw', bots))
            setNameEdited(false)
            setRemote('')
            setRateLimit('')
            setCreated(null)
        }
    }

    const limit = parseRateLimitInput(rateLimit)
    const submit = () => {
        if (!name.trim() || surfaces.length === 0 || !limit.ok) return
        let key = ''
        create.mutate(
            {
                name: name.trim(),
                kind,
                surfaces,
                ...(remote ? { remote_agent: remote } : {}),
                ...(limit.value !== null ? { rate_limit_rpm: limit.value } : {}),
                onKey: (value) => {
                    key = value
                },
            },
            { onSuccess: (bot) => setCreated({ bot, key }) },
        )
    }

    const entry = kindEntry(kind)
    const showRemote = Boolean(entry?.exposesA2a) && remotes.length > 0

    const formId = 'connect-bot-form'

    return (
        <>
            <BotSheet
                open={open}
                onOpenChange={(value) => {
                    if (value) onOpenChange(true)
                    // Closing mid-request would drop the key the gateway is about to return.
                    else if (!create.isPending) guard.request(Boolean(created))
                }}
                kind={created ? created.bot.kind : undefined}
                title={created ? `Set up ${created.bot.name}` : 'Connect a bot'}
                description={
                    created
                        ? `Paste this into ${kindEntry(created.bot.kind)?.name ?? 'the bot'}. It shows as connected after its first call.`
                        : 'The bot gets its own key that can only use what you allow here.'
                }
                bodyRef={bodyRef}
                footer={
                    created ? (
                        <BotSheetActions>
                            <Button onClick={() => guard.request(true)}>Done</Button>
                        </BotSheetActions>
                    ) : (
                        <BotSheetActions
                            error={create.isError ? `Could not connect the bot. ${describeBotError(create.error)}` : undefined}
                        >
                            <Button type="button" variant="outline" onClick={close} disabled={create.isPending}>
                                Cancel
                            </Button>
                            <Button
                                type="submit"
                                form={formId}
                                disabled={create.isPending || !name.trim() || surfaces.length === 0 || !limit.ok}
                            >
                                {create.isPending ? 'Connecting…' : 'Connect bot'}
                            </Button>
                        </BotSheetActions>
                    )
                }
            >
                {!created ? (
                    <form
                        id={formId}
                        className="min-w-0 space-y-6"
                        onSubmit={(event) => {
                            event.preventDefault()
                            submit()
                        }}
                    >
                        <fieldset className="min-w-0 space-y-2">
                            <legend className="mb-2 text-sm font-medium">Type</legend>
                            <div role="radiogroup" aria-label="Bot type" className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                                {BOT_CATALOG.map((option) => {
                                    const selected = option.id === kind
                                    return (
                                        <button
                                            key={option.id}
                                            type="button"
                                            role="radio"
                                            aria-checked={selected}
                                            title={option.description}
                                            onClick={() => chooseKind(option.id)}
                                            className={`flex min-w-0 items-center gap-2.5 rounded border px-2.5 py-2 text-left transition-colors ${selected ? 'border-brand-secondary-500/50 bg-brand-secondary-600/20' : 'border-brand-main-600/40 hover:bg-brand-main-800/60'}`}
                                        >
                                            <BotLogo kind={option.id} />
                                            <span className="min-w-0 break-words text-sm leading-4 text-brand-secondary-100">
                                                {option.name}
                                            </span>
                                        </button>
                                    )
                                })}
                            </div>
                            {entry && (
                                <p className="flex flex-wrap items-center gap-x-2 gap-y-1 pt-1 text-xs leading-5 text-brand-main-300">
                                    <span>{entry.description}</span>
                                    {entry.hosted && (
                                        <Badge variant="secondary" className="shrink-0 text-[10px]">
                                            Hosted by {entry.hosted}
                                        </Badge>
                                    )}
                                </p>
                            )}
                        </fieldset>
                        <div className="space-y-2">
                            <Label htmlFor="bot-name">Name</Label>
                            <Input
                                id="bot-name"
                                value={name}
                                onChange={(event) => {
                                    setName(event.target.value)
                                    setNameEdited(true)
                                }}
                                maxLength={64}
                                required
                                className={inputClass}
                            />
                        </div>
                        <SurfaceChecklist kind={kind} value={surfaces} onChange={setSurfaces} idPrefix="connect" />
                        {showRemote && <RemoteSelect remotes={remotes} value={remote} onChange={setRemote} />}
                        <RateLimitField value={rateLimit} onChange={setRateLimit} idPrefix="connect" />
                    </form>
                ) : (
                    <KeyReveal
                        apiKey={created.key}
                        apiKeyId={created.bot.api_key_id}
                        kind={created.bot.kind}
                        surfaces={created.bot.surfaces}
                        onCopied={guard.markCopied}
                    />
                )}
            </BotSheet>
            {guard.confirm}
        </>
    )
}

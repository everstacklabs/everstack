import { useState, useEffect } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { toast } from '@everstack/ui/components'
import { useAgents } from '@/hooks/deployments/use-agents'
import { useCreateChannel, useUpdateChannel, useChannel, usePlatformChannels } from '@/hooks/deployments/use-channels'
import { Platform, SessionMode } from '@/server/channels'
import type { JsonObject } from '@everstack/client'

const {
    Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetBody,
    Button, Input, Label,
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
    Switch,
    Tabs, TabsList, TabsTrigger, TabsContent,
} = ui

interface ChannelConfigSheetProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    mode: 'create' | 'edit'
    channelId?: string
}

const PLATFORM_OPTIONS = [
    { value: String(Platform.DISCORD), label: 'Discord', icon: 'simple-icons:discord' },
    { value: String(Platform.SLACK), label: 'Slack', icon: 'simple-icons:slack' },
    { value: String(Platform.TELEGRAM), label: 'Telegram', icon: 'simple-icons:telegram' },
]

const SESSION_MODE_OPTIONS = [
    { value: String(SessionMode.THREAD), label: 'Thread', description: 'Shared in main channel, per-user in threads' },
    { value: String(SessionMode.SHARED), label: 'Shared', description: 'One session per channel, all users contribute' },
    { value: String(SessionMode.PER_USER), label: 'Per User', description: 'Each user gets their own session' },
]

export function ChannelConfigSheet({ open, onOpenChange, mode, channelId }: ChannelConfigSheetProps) {
    const { data: agents } = useAgents()
    const createChannel = useCreateChannel()
    const updateChannel = useUpdateChannel()
    const existingChannel = useChannel(channelId ?? '')

    const [activeTab, setActiveTab] = useState('general')

    // General
    const [name, setName] = useState('')
    const [platform, setPlatform] = useState(String(Platform.DISCORD))

    const isTelegram = Number(platform) === Platform.TELEGRAM
    const canListChannels = mode === 'edit' && !!channelId && !isTelegram
    const { data: platformChannels, isLoading: platformChannelsLoading } = usePlatformChannels(canListChannels ? channelId! : '')
    const [agentId, setAgentId] = useState<string | null>(null)
    const [sessionMode, setSessionMode] = useState(String(SessionMode.THREAD))
    const [enabled, setEnabled] = useState(true)

    // Credentials
    const [botToken, setBotToken] = useState('')
    const [appToken, setAppToken] = useState('')

    // Advanced
    const [allowedChannels, setAllowedChannels] = useState('')
    const [mentionPrefix, setMentionPrefix] = useState('')
    const [maxMessagesPerMinute, setMaxMessagesPerMinute] = useState(30)
    const [maxResponseLength, setMaxResponseLength] = useState(2000)
    const [webSearchMode, setWebSearchMode] = useState<'auto' | 'on' | 'off'>('off')
    const [webSearchAutoRetry, setWebSearchAutoRetry] = useState(true)

    useEffect(() => {
        if (mode === 'edit' && existingChannel) {
            setName(existingChannel.name)
            setPlatform(String(existingChannel.platform))
            setAgentId(existingChannel.agentId ?? null)
            setSessionMode(String(existingChannel.sessionMode))
            setEnabled(existingChannel.enabled)
            setMaxMessagesPerMinute(existingChannel.maxMessagesPerMinute)
            setMaxResponseLength(existingChannel.maxResponseLength)

            const pc = existingChannel.platformConfig as JsonObject | undefined
            if (pc) {
                if (Array.isArray(pc.allowed_channels)) {
                    setAllowedChannels(pc.allowed_channels.join(', '))
                }
                if (typeof pc.mention_prefix === 'string') {
                    setMentionPrefix(pc.mention_prefix)
                }
                if (typeof pc.web_search_mode === 'string') {
                    const mode = pc.web_search_mode.toLowerCase()
                    if (mode === 'auto' || mode === 'on' || mode === 'off') {
                        setWebSearchMode(mode)
                    }
                } else if (typeof pc.web_search_enabled === 'boolean') {
                    setWebSearchMode(pc.web_search_enabled ? 'on' : 'off')
                }
                if (typeof pc.web_search_auto_retry === 'boolean') {
                    setWebSearchAutoRetry(pc.web_search_auto_retry)
                }
            }
        }
    }, [mode, existingChannel])

    // Reset form on close
    useEffect(() => {
        if (!open) {
            setActiveTab('general')
            setBotToken('')
            setAppToken('')
            if (mode === 'create') {
                setName('')
                setPlatform(String(Platform.DISCORD))
                setAgentId('')
                setSessionMode(String(SessionMode.THREAD))
                setEnabled(true)
                setAllowedChannels('')
                setMentionPrefix('')
                setMaxMessagesPerMinute(30)
                setMaxResponseLength(2000)
                setWebSearchMode('off')
                setWebSearchAutoRetry(true)
            }
        }
    }, [open, mode])

    useEffect(() => {
        if (!open || mode !== 'create') return
        if (Number(platform) === Platform.SLACK) {
            setWebSearchMode('auto')
            setWebSearchAutoRetry(true)
        } else {
            setWebSearchMode('off')
        }
    }, [open, mode, platform])

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()

        const platformConfig: JsonObject = {}
        if (allowedChannels.trim()) {
            platformConfig.allowed_channels = allowedChannels.split(',').map((s) => s.trim()).filter(Boolean)
        }
        if (mentionPrefix.trim()) {
            platformConfig.mention_prefix = mentionPrefix.trim()
        }
        platformConfig.web_search_mode = webSearchMode
        platformConfig.web_search_auto_retry = webSearchAutoRetry

        const credentials: JsonObject = {}
        if (botToken.trim()) credentials.bot_token = botToken.trim()
        if (appToken.trim()) credentials.app_token = appToken.trim()

        try {
            if (mode === 'create') {
                await createChannel.mutateAsync({
                    name,
                    platform: Number(platform) as Platform,
                    agentId: agentId ?? '',
                    sessionMode: Number(sessionMode) as SessionMode,
                    credentials: Object.keys(credentials).length > 0 ? credentials : undefined,
                    platformConfig: Object.keys(platformConfig).length > 0 ? platformConfig : undefined,
                    maxMessagesPerMinute,
                    maxResponseLength,
                })
                toast.success('Channel created successfully')
            } else if (channelId) {
                await updateChannel.mutateAsync({
                    id: channelId,
                    name,
                    agentId: agentId ?? undefined,
                    enabled,
                    sessionMode: Number(sessionMode) as SessionMode,
                    credentials: botToken.trim() || appToken.trim() ? credentials : undefined,
                    platformConfig: Object.keys(platformConfig).length > 0 ? platformConfig : undefined,
                    maxMessagesPerMinute,
                    maxResponseLength,
                })
                toast.success('Channel updated successfully')
            }
            onOpenChange(false)
        } catch (error) {
            toast.error(`Failed to ${mode} channel: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const isPending = createChannel.isPending || updateChannel.isPending
    const selectedPlatform = PLATFORM_OPTIONS.find(p => p.value === platform)
    const isSlack = Number(platform) === Platform.SLACK

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent className="bg-brand-main-900 border-l-brand-main-500 w-full sm:max-w-[500px]">
                <SheetHeader className="flex items-center space-x-2.5">
                    <SheetTitle className="text-white light:text-brand-main-50 text-base font-semibold flex items-center gap-2">
                        {selectedPlatform && (
                            <Iconify.Icon icon={selectedPlatform.icon} className="h-5 w-5" />
                        )}
                        <span>{mode === 'create' ? 'Add Channel' : `Edit ${name || 'Channel'}`}</span>
                    </SheetTitle>
                    <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
                        {mode === 'create'
                            ? 'Connect an agent to a messaging platform.'
                            : 'Update channel configuration and credentials.'}
                    </SheetDescription>
                </SheetHeader>

                <SheetBody className="py-4">
                    <form onSubmit={handleSubmit} className="space-y-4">
                        <Tabs value={activeTab} onValueChange={setActiveTab} className="space-y-4">
                            <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                                <TabsTrigger
                                    value="general"
                                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
                                >
                                    General
                                </TabsTrigger>
                                <TabsTrigger
                                    value="credentials"
                                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
                                >
                                    Credentials
                                </TabsTrigger>
                                <TabsTrigger
                                    value="advanced"
                                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
                                >
                                    Advanced
                                </TabsTrigger>
                            </TabsList>

                            {/* General Tab */}
                            <TabsContent value="general" className="space-y-4 mt-0">
                                <div className="space-y-4">
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">
                                            Name <span className="text-red-400 light:text-red-600">*</span>
                                        </Label>
                                        <Input
                                            placeholder="e.g., Discord Support Bot"
                                            value={name}
                                            onChange={(e) => setName(e.target.value)}
                                            className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                            required
                                        />
                                    </div>

                                    {mode === 'create' && (
                                        <div className="space-y-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">
                                                Platform <span className="text-red-400 light:text-red-600">*</span>
                                            </Label>
                                            <Select value={platform} onValueChange={setPlatform}>
                                                <SelectTrigger className="w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                    <SelectValue />
                                                </SelectTrigger>
                                                <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                    {PLATFORM_OPTIONS.map((opt) => (
                                                        <SelectItem key={opt.value} value={opt.value}>
                                                            <span className="inline-flex items-center gap-2">
                                                                <Iconify.Icon icon={opt.icon} className="h-4 w-4" />
                                                                {opt.label}
                                                            </span>
                                                        </SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                        </div>
                                    )}

                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Agent</Label>
                                        <Select value={agentId || '__dispatcher__'} onValueChange={(v) => setAgentId(v === '__dispatcher__' ? '' : v)}>
                                            <SelectTrigger className="w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                <SelectValue placeholder="Select an agent..." />
                                            </SelectTrigger>
                                            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                <SelectItem value="__dispatcher__">
                                                    <span className="inline-flex items-center gap-2">
                                                        <Iconify.Icon icon="lucide:route" className="h-4 w-4 text-amber-400 light:text-amber-700" />
                                                        Dispatcher mode (auto-route)
                                                    </span>
                                                </SelectItem>
                                                {agents?.map((agent) => (
                                                    <SelectItem key={agent.id} value={agent.id}>
                                                        {agent.name}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <p className="text-xs text-white/40 light:text-black/40">
                                            {agentId
                                                ? 'Messages will be routed to this agent. Users can still mention other agents by name.'
                                                : 'No default agent. Messages will be auto-routed with confirmation, or users can mention an agent by name.'}
                                        </p>
                                    </div>

                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Session Mode</Label>
                                        <Select value={sessionMode} onValueChange={setSessionMode}>
                                            <SelectTrigger className="w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                {SESSION_MODE_OPTIONS.map((opt) => (
                                                    <SelectItem key={opt.value} value={opt.value}>
                                                        {opt.label}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <p className="text-xs text-white/40 light:text-black/40">
                                            {SESSION_MODE_OPTIONS.find(o => o.value === sessionMode)?.description}
                                        </p>
                                    </div>

                                    {mode === 'edit' && (
                                        <div className="flex items-center justify-between py-2 px-3 rounded-md bg-brand-main-800/50 border border-brand-main-600">
                                            <div>
                                                <Label className="text-white light:text-brand-main-50 font-medium">Enabled</Label>
                                                <p className="text-xs text-white/40 light:text-black/40">Toggle channel on/off without deleting</p>
                                            </div>
                                            <Switch checked={enabled} onCheckedChange={setEnabled} />
                                        </div>
                                    )}
                                </div>
                            </TabsContent>

                            {/* Credentials Tab */}
                            <TabsContent value="credentials" className="space-y-4 mt-0">
                                <div className="space-y-4">
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">
                                            Bot Token {mode === 'create' && <span className="text-red-400 light:text-red-600">*</span>}
                                        </Label>
                                        <Input
                                            type="password"
                                            placeholder={mode === 'edit' ? '(unchanged — enter to replace)' : 'Enter bot token...'}
                                            value={botToken}
                                            onChange={(e) => setBotToken(e.target.value)}
                                            className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm font-mono"
                                        />
                                        <p className="text-xs text-white/40 light:text-black/40">
                                            {Number(platform) === Platform.DISCORD && 'From Discord Developer Portal → Bot → Token'}
                                            {Number(platform) === Platform.SLACK && 'Bot User OAuth Token (xoxb-...) from Slack App settings'}
                                            {Number(platform) === Platform.TELEGRAM && 'Token from @BotFather on Telegram'}
                                        </p>
                                    </div>

                                    {isSlack && (
                                        <div className="space-y-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">
                                                App-Level Token {mode === 'create' && <span className="text-red-400 light:text-red-600">*</span>}
                                            </Label>
                                            <Input
                                                type="password"
                                                placeholder={mode === 'edit' ? '(unchanged — enter to replace)' : 'xapp-...'}
                                                value={appToken}
                                                onChange={(e) => setAppToken(e.target.value)}
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm font-mono"
                                            />
                                            <p className="text-xs text-white/40 light:text-black/40">
                                                Required for Socket Mode. From Slack App → Basic Info → App-Level Tokens.
                                            </p>
                                        </div>
                                    )}

                                    <div className="rounded-md border border-brand-main-600 bg-brand-main-800/30 p-3">
                                        <div className="flex items-start gap-2">
                                            <Iconify.Icon icon="lucide:shield-check" className="h-4 w-4 text-emerald-400 light:text-emerald-600 mt-0.5 shrink-0" />
                                            <p className="text-xs text-white/50 light:text-black/50">
                                                Credentials are encrypted at rest using AES-256-GCM and are never returned in API responses.
                                            </p>
                                        </div>
                                    </div>
                                </div>
                            </TabsContent>

                            {/* Advanced Tab */}
                            <TabsContent value="advanced" className="space-y-4 mt-0">
                                <div className="space-y-4">
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Allowed Channels</Label>
                                        {canListChannels && platformChannels && platformChannels.length > 0 ? (
                                            <div className="space-y-2 max-h-48 overflow-y-auto scrollbar-macos rounded-md border border-brand-main-600 bg-brand-main-800/30 p-2">
                                                {platformChannels.map(ch => {
                                                    const selected = allowedChannels.split(',').map(s => s.trim()).filter(Boolean)
                                                    const isChecked = selected.includes(ch.id)
                                                    return (
                                                        <label
                                                            key={ch.id}
                                                            className="flex items-center gap-2.5 rounded px-2 py-1.5 cursor-pointer transition-colors hover:bg-brand-main-700/40"
                                                        >
                                                            <input
                                                                type="checkbox"
                                                                checked={isChecked}
                                                                onChange={e => {
                                                                    const updated = e.target.checked
                                                                        ? [...selected, ch.id]
                                                                        : selected.filter(id => id !== ch.id)
                                                                    setAllowedChannels(updated.join(', '))
                                                                }}
                                                                className="rounded border-brand-main-600 bg-brand-main-900"
                                                            />
                                                            <span className="text-sm text-white light:text-brand-main-50 truncate">{ch.name}</span>
                                                            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-brand-main-700/50 text-brand-main-200">
                                                                {ch.type}
                                                            </span>
                                                        </label>
                                                    )
                                                })}
                                            </div>
                                        ) : canListChannels && platformChannelsLoading ? (
                                            <p className="text-xs text-white/40 light:text-black/40">Loading channels...</p>
                                        ) : (
                                            <Input
                                                placeholder="Channel IDs, comma-separated (empty = all)"
                                                value={allowedChannels}
                                                onChange={(e) => setAllowedChannels(e.target.value)}
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                            />
                                        )}
                                        <p className="text-xs text-white/40 light:text-black/40">
                                            {canListChannels && platformChannels && platformChannels.length > 0
                                                ? 'Select which channels the bot should respond in. Unchecked = all channels.'
                                                : mode === 'create'
                                                    ? 'Save first, then edit to select from available channels.'
                                                    : 'Leave empty to allow the bot to respond in all channels.'}
                                        </p>
                                    </div>

                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Web Search Policy</Label>
                                        <Select value={webSearchMode} onValueChange={(value) => setWebSearchMode(value as 'auto' | 'on' | 'off')}>
                                            <SelectTrigger className="w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                <SelectValue placeholder="Select web search policy" />
                                            </SelectTrigger>
                                            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                                <SelectItem value="auto">Auto (detect intent)</SelectItem>
                                                <SelectItem value="on">On (always enabled)</SelectItem>
                                                <SelectItem value="off">Off</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <p className="text-xs text-white/40 light:text-black/40">
                                            Auto enables web search when users explicitly ask for it (recommended for Slack).
                                        </p>
                                    </div>

                                    <div className="flex items-start justify-between gap-4 rounded-lg border border-brand-main-700/40 bg-brand-main-900/40 px-3 py-2">
                                        <div className="space-y-1">
                                            <Label className="text-white light:text-brand-main-50 font-medium">Web Search Auto-Retry</Label>
                                            <p className="text-xs text-white/40 light:text-black/40">
                                                If a message requests web search but the policy would block it, retry the turn with web search enabled.
                                            </p>
                                        </div>
                                        <Switch checked={webSearchAutoRetry} onCheckedChange={setWebSearchAutoRetry} />
                                    </div>

                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Mention Prefix</Label>
                                        <Input
                                            placeholder='e.g., "!ask" or "/agent"'
                                            value={mentionPrefix}
                                            onChange={(e) => setMentionPrefix(e.target.value)}
                                            className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                        />
                                        <p className="text-xs text-white/40 light:text-black/40">
                                            Alternative to @mention — bot responds to messages starting with this prefix.
                                        </p>
                                    </div>

                                    <div className="grid grid-cols-2 gap-4">
                                        <div className="space-y-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">Rate Limit</Label>
                                            <Input
                                                type="number"
                                                value={maxMessagesPerMinute}
                                                onChange={(e) => setMaxMessagesPerMinute(Number(e.target.value))}
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                            />
                                            <p className="text-xs text-white/40 light:text-black/40">Messages per minute</p>
                                        </div>
                                        <div className="space-y-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">Max Response</Label>
                                            <Input
                                                type="number"
                                                value={maxResponseLength}
                                                onChange={(e) => setMaxResponseLength(Number(e.target.value))}
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                            />
                                            <p className="text-xs text-white/40 light:text-black/40">Characters per message</p>
                                        </div>
                                    </div>
                                </div>
                            </TabsContent>

                            <Button
                                type="submit"
                                className="w-full"
                                disabled={isPending || !name}
                            >
                                {isPending ? 'Saving...' : mode === 'create' ? 'Create Channel' : 'Save Changes'}
                            </Button>
                        </Tabs>
                    </form>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}

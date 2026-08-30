import { useState } from 'react'
import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Slider, Textarea, Tabs, TabsList, TabsTrigger, TabsContent } from '@everstack/ui/components'
import type { VoiceCloneConfig } from '../../types'
import { useVoiceProfiles } from '@/hooks/deployments/use-voice'

interface Props { config: VoiceCloneConfig; onChange: (config: VoiceCloneConfig) => void }

const tabCls = "relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3"
const sliderCls = "[&_[data-slot=slider-track]]:h-1 [&_[data-slot=slider-track]]:bg-brand-main-700 [&_[data-slot=slider-range]]:bg-brand-secondary-500 [&_[data-slot=slider-thumb]]:size-3 [&_[data-slot=slider-thumb]]:border-brand-secondary-500 [&_[data-slot=slider-thumb]]:bg-brand-secondary-400"

export function VoiceCloneConfigForm({ config, onChange }: Props) {
    const { data: voiceProfiles } = useVoiceProfiles()
    const [tab, setTab] = useState('general')

    return (
        <Tabs value={tab} onValueChange={setTab} className="space-y-4">
            <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                <TabsTrigger value="general" className={tabCls}>General</TabsTrigger>
                <TabsTrigger value="audio" className={tabCls}>Audio</TabsTrigger>
                <TabsTrigger value="post" className={tabCls}>Post</TabsTrigger>
            </TabsList>

            <TabsContent value="general" className="space-y-4 mt-0 scrollbar-macos">
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Input Text</Label>
                    <Textarea
                        value={config.inputText}
                        onChange={(e) => onChange({ ...config, inputText: e.target.value })}
                        className="bg-brand-main-700/50 max-h-20 border-brand-main-500 text-white light:text-brand-main-50 font-mono text-xs"
                        placeholder="Text to synthesize with the cloned voice. Empty = previous node output."
                        rows={3}
                    />
                    <p className="text-xs text-brand-main-400">
                        Long text is automatically chunked. Empty = previous node output.
                    </p>
                </div>
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Provider</Label>
                    <Select value={config.provider} onValueChange={(v) => onChange({ ...config, provider: v })}>
                        <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="qwen">Qwen</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Model</Label>
                    <Select value={config.model} onValueChange={(v) => onChange({ ...config, model: v })}>
                        <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="qwen3-tts-vc-2026-01-22">qwen3-tts-vc-2026-01-22</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Voice Clone Profile</Label>
                    {voiceProfiles && voiceProfiles.length > 0 ? (
                        <Select
                            value={config.voiceCloneProfileId || 'none'}
                            onValueChange={(v) => onChange({ ...config, voiceCloneProfileId: v === 'none' ? '' : v })}
                        >
                            <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                                <SelectValue placeholder="Select a profile" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="none">None</SelectItem>
                                {voiceProfiles.map((p) => (
                                    <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    ) : (
                        <p className="text-xs text-brand-main-500 py-1">
                            No voice profiles. Create one in the Voice page.
                        </p>
                    )}
                </div>
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Preferred Name</Label>
                    <Input
                        value={config.preferredName}
                        onChange={(e) => onChange({ ...config, preferredName: e.target.value })}
                        className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                        placeholder="Name for the cloned voice"
                    />
                </div>
            </TabsContent>

            <TabsContent value="audio" className="space-y-4 mt-0 scrollbar-macos">
                <div className="rounded-md border border-brand-main-600 bg-brand-main-800/30 p-2.5">
                    <p className="text-[10px] text-brand-main-400">
                        Qwen voice clone models have limited audio parameter support.
                        Speed and style are controlled via <span className="text-brand-secondary-300">Instructions</span> (natural language).
                        Other parameters are reserved for future providers.
                    </p>
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Speed ({(config.speed || 1.0).toFixed(1)}x)</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.speed || 1.0]}
                        onValueChange={([v]) => onChange({ ...config, speed: v })}
                        min={0.5}
                        max={2.0}
                        step={0.1}
                    />
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Instructions</Label>
                    <Textarea
                        value={config.instructions}
                        onChange={(e) => onChange({ ...config, instructions: e.target.value })}
                        className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs"
                        placeholder="Speak with a warm, friendly tone and moderate pace..."
                        rows={2}
                    />
                    <p className="text-xs text-brand-main-400">
                        Natural language control for pitch, speed, emotion, and personality.
                    </p>
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Temperature ({(config.temperature || 0).toFixed(1)})</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.temperature || 0]}
                        onValueChange={([v]) => onChange({ ...config, temperature: v })}
                        min={0}
                        max={1.0}
                        step={0.1}
                    />
                    <p className="text-xs text-brand-main-400">Randomness in generation. 0 = deterministic.</p>
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Top P ({(config.topP || 0).toFixed(1)})</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.topP || 0]}
                        onValueChange={([v]) => onChange({ ...config, topP: v })}
                        min={0}
                        max={1.0}
                        step={0.05}
                    />
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Stability ({(config.stability || 0).toFixed(1)})</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.stability || 0]}
                        onValueChange={([v]) => onChange({ ...config, stability: v })}
                        min={0}
                        max={1.0}
                        step={0.05}
                    />
                    <p className="text-xs text-brand-main-400">Higher = more consistent. Lower = more expressive.</p>
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Similarity ({(config.similarity || 0).toFixed(1)})</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.similarity || 0]}
                        onValueChange={([v]) => onChange({ ...config, similarity: v })}
                        min={0}
                        max={1.0}
                        step={0.05}
                    />
                    <p className="text-xs text-brand-main-400">How closely the output matches the original voice.</p>
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Style ({(config.style || 0).toFixed(1)})</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.style || 0]}
                        onValueChange={([v]) => onChange({ ...config, style: v })}
                        min={0}
                        max={1.0}
                        step={0.05}
                    />
                    <p className="text-xs text-brand-main-400">Style exaggeration. Higher = more dramatic delivery.</p>
                </div>
            </TabsContent>

            <TabsContent value="post" className="space-y-4 mt-0 scrollbar-macos">
                <div className="flex items-start justify-between gap-4 rounded-md border border-brand-main-700/40 bg-brand-main-900/40 px-3 py-2.5">
                    <div className="space-y-0.5">
                        <Label className="text-sm text-brand-main-200">Audio Enhancement</Label>
                        <p className="text-xs text-brand-main-400">
                            Normalize volume and apply noise gate to reduce background noise.
                        </p>
                    </div>
                    <button
                        type="button"
                        onClick={() => onChange({ ...config, enhancement: !config.enhancement })}
                        className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full transition-colors ${config.enhancement ? 'bg-brand-secondary-500' : 'bg-brand-main-700'}`}
                    >
                        <span className={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-sm ring-0 transition-transform mt-0.5 ${config.enhancement ? 'translate-x-4 ml-0.5' : 'translate-x-0.5'}`} />
                    </button>
                </div>

                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Speaker Boost ({(config.speakerBoost || 0).toFixed(1)})</Label>
                    <Slider
                        className={sliderCls}
                        value={[config.speakerBoost || 0]}
                        onValueChange={([v]) => onChange({ ...config, speakerBoost: v })}
                        min={0}
                        max={1.0}
                        step={0.05}
                    />
                    <p className="text-xs text-brand-main-400">
                        Boost speaker volume with soft clipping. 0 = off, 1 = max boost (~6dB).
                    </p>
                </div>
            </TabsContent>
        </Tabs>
    )
}

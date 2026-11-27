import { Iconify } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import type { BotKind } from './bot-catalog'

// Each bot's own mark, copied into public/bots so a self-hosted dashboard never
// loads an image from a third party. Fetched 2026-11-10 from:
//   openclaw, hermes, grok-*, meta-muse: lobehub/lobe-icons static-svg (MIT), the
//     set /providers/lobehub already comes from. Icons OpenClaw (color),
//     HermesAgent, Grok and Muse (color).
//   nanobot: HKUDS/nanobot, webui/public/brand/nanobot_icon_192.png (MIT).
//   zeroclaw: zeroclaw-labs/zeroclaw, web/public/logo.png (Apache-2.0), scaled
//     to 128px.
//   nanoclaw: nanocoai/nanoclaw, assets/nanoclaw-icon.png (MIT), empty margin
//     cropped, scaled to 128px.
//   copilotkit-openbot: CopilotKit/OpenBot, app/src/assets/openbot-logo.png (MIT).
// The licences above are each repo's code licence. The names and marks stay
// their owners' trademarks (ZeroClaw's README says so in terms) and are shown
// only to name the bot a row is about.
// nightly-labs/openbot publishes its icon under PolyForm Noncommercial, so that
// kind shows the generic icon. So do custom bots and any kind this build does
// not know.

export interface BotLogoAsset {
    src: string
    /** A single-colour mark drawn in black, recoloured to match the theme. */
    mono?: boolean
    /** An opaque image that fills the tile instead of sitting inside it. */
    fill?: boolean
}

export const BOT_LOGOS: Partial<Record<BotKind, BotLogoAsset>> = {
    openclaw: { src: '/bots/openclaw.svg' },
    hermes: { src: '/bots/hermes-agent.svg', mono: true },
    nanobot: { src: '/bots/nanobot.png' },
    zeroclaw: { src: '/bots/zeroclaw.png' },
    nanoclaw: { src: '/bots/nanoclaw.png', fill: true },
    'copilotkit-openbot': { src: '/bots/copilotkit-openbot.png' },
    'grok-bot': { src: '/bots/grok.svg', mono: true },
    'grok-build': { src: '/bots/grok.svg', mono: true },
    'meta-muse': { src: '/bots/meta-muse.svg' },
}

const TILE = {
    sm: 'size-6 rounded-md',
    md: 'size-8 rounded-md',
}

/** A fixed-size tile with the bot's mark, or a generic icon when there is none.
 *  Decorative: the bot's name always sits next to it. */
export function BotLogo({ kind, size = 'sm', className }: { kind: string; size?: keyof typeof TILE; className?: string }) {
    const asset = BOT_LOGOS[kind as BotKind]
    return (
        <span
            aria-hidden="true"
            data-bot-logo={asset ? kind : 'generic'}
            className={cn(
                'inline-flex shrink-0 items-center justify-center overflow-hidden border border-brand-main-600/40 bg-brand-main-900/60',
                TILE[size],
                className,
            )}
        >
            {asset ? (
                <img
                    src={asset.src}
                    alt=""
                    draggable={false}
                    className={cn(
                        asset.fill ? 'size-full object-cover' : 'size-[72%] object-contain',
                        asset.mono && 'brightness-0 invert light:invert-0',
                    )}
                />
            ) : (
                <Iconify.Icon icon="lucide:bot" className="size-[64%] text-brand-main-200" />
            )}
        </span>
    )
}

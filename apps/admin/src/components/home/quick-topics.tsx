import { Link } from '@tanstack/react-router'
import { Iconify } from '@everstack/ui/icons'

// The shortcuts under the assistant composer. Each one does something real:
// a topic the assistant has tools for drops a starter prompt into the
// composer, ready to send; every other topic opens that part of the product.
// The assistant manages agents only, so only Agents is a prompt. Give a topic
// a prompt here once the assistant has the tools to act on it.
const QUICK_TOPICS = [
  {
    label: 'Agents',
    icon: 'lucide:bot',
    prompt: 'List my agents and what each one does.',
  },
  {
    label: 'Observability',
    icon: 'hugeicons:telescope-02',
    to: '/observability/traces',
  },
  { label: 'Gateway', icon: 'mingcute:route-line', to: '/gateway' },
  { label: 'Evaluations', icon: 'lucide:flask-conical', to: '/evaluations' },
  { label: 'Vault', icon: 'stash:vault', to: '/vault/api-keys' },
  { label: 'Sandbox', icon: 'lucide:box', to: '/deployments/sandboxes' },
] as const

const PILL =
  'flex items-center gap-1.5 rounded-full border border-brand-main-600 bg-brand-main-800/40 px-3 py-1.5 text-xs text-white/50 light:text-black/50 transition-colors hover:border-brand-main-500 hover:text-white/70 light:hover:text-black/70 hover:bg-brand-main-800/60'

export function QuickTopics({
  onPrompt,
}: {
  // Puts a starter prompt in the composer and focuses it.
  onPrompt: (prompt: string) => void
}) {
  return (
    <div className="flex flex-wrap items-center justify-center gap-2">
      {QUICK_TOPICS.map((topic) =>
        'prompt' in topic ? (
          <button
            key={topic.label}
            type="button"
            onClick={() => onPrompt(topic.prompt)}
            className={PILL}
          >
            <Iconify.Icon icon={topic.icon} className="size-3.5" />
            {topic.label}
          </button>
        ) : (
          <Link
            key={topic.label}
            to={topic.to}
            className={PILL}
          >
            <Iconify.Icon icon={topic.icon} className="size-3.5" />
            {topic.label}
            <Iconify.Icon
              icon="lucide:arrow-up-right"
              className="size-3 opacity-60"
            />
          </Link>
        ),
      )}
    </div>
  )
}

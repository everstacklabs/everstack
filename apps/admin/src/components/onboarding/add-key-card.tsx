import {
  useId,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import { Iconify } from '@everstack/ui/icons'
import { Button, Input } from '@everstack/ui/components'
import { cn } from '@everstack/utils/functions/cn'
import { Code, ConnectError } from '@everstack/client'
import { useConfigureProvider } from '@/hooks/vault/use-providers'
import {
  cheapModelFor,
  DEFAULT_KEY_PROVIDER,
  keyModelFor,
  type KeyProvider,
} from '@/lib/first-request'

// The providers an org can add from the playground (founder decision 8).
const KEY_PROVIDERS: readonly {
  name: KeyProvider
  label: string
  icon: string
  placeholder: string
}[] = [
  {
    name: 'openai',
    label: 'OpenAI',
    icon: 'simple-icons:openai',
    placeholder: 'sk-...',
  },
  {
    name: 'anthropic',
    label: 'Anthropic',
    icon: 'simple-icons:anthropic',
    placeholder: 'sk-ant-...',
  },
]

export interface AddedKey {
  provider: KeyProvider
  // Gateway model reference the provider was enabled with.
  model: string
}

// A validation refusal from the gateway (wrong key shape, say) is worth
// repeating; anything else gets a plain line rather than a transport code.
function saveErrorMessage(err: unknown): string {
  if (
    err instanceof ConnectError &&
    (err.code === Code.InvalidArgument ||
      err.code === Code.FailedPrecondition) &&
    err.rawMessage
  ) {
    return err.rawMessage
  }
  return 'The key could not be saved. Check it and try again.'
}

// AddKeyCard is the inline "add your own key" step the playground shows when
// free requests cannot serve the next message (used up, expired, paused, or
// unsupported input), or when there were none to begin with. It configures
// the provider through the same mutation the provider sheet uses, enabling
// its cheap model so the resend can route immediately. The key goes straight
// to the configure call; it is never kept in state after submit, logged, or
// echoed.
//
// The provider choice can be lifted (`provider` + `onProviderChange`) so the
// quickstart snippet names the model this card will enable.
export function AddKeyCard({
  title,
  saveLabel = 'Add key and resend',
  onSaved,
  provider: controlledProvider,
  onProviderChange,
  onOtherProvider,
  autoFocus = false,
  className,
}: {
  title: string
  saveLabel?: string
  onSaved: (added: AddedKey) => void
  provider?: KeyProvider
  onProviderChange?: (provider: KeyProvider) => void
  // Opens the full provider list, for keys other than OpenAI or Anthropic.
  onOtherProvider?: () => void
  // Moves focus to the key field on mount: set when the card replaces the
  // message box the user was typing in.
  autoFocus?: boolean
  className?: string
}) {
  const configure = useConfigureProvider()
  const [localProvider, setLocalProvider] =
    useState<KeyProvider>(DEFAULT_KEY_PROVIDER)
  const provider = controlledProvider ?? localProvider
  const setProvider = (next: KeyProvider) => {
    setLocalProvider(next)
    onProviderChange?.(next)
  }
  const [apiKey, setApiKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const radios = useRef<(HTMLButtonElement | null)[]>([])
  const active = KEY_PROVIDERS.find((p) => p.name === provider)!
  const hintId = useId()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const key = apiKey.trim()
    if (!key || configure.isPending) return
    setError(null)
    const model = cheapModelFor(provider)
    try {
      await configure.mutateAsync({
        providerName: provider,
        apiKey: key,
        apiKeyName: 'Default',
        apiKeyWeight: 1,
        enabledModels: model ? [model] : [],
      })
      setApiKey('')
      onSaved({ provider, model: keyModelFor(provider) })
    } catch (err) {
      setError(saveErrorMessage(err))
    }
  }

  // Radio group keyboard pattern: one tab stop, arrows move the choice.
  const onRadioKey = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const step =
      e.key === 'ArrowRight' || e.key === 'ArrowDown'
        ? 1
        : e.key === 'ArrowLeft' || e.key === 'ArrowUp'
          ? -1
          : 0
    if (step === 0) return
    e.preventDefault()
    const next = (index + step + KEY_PROVIDERS.length) % KEY_PROVIDERS.length
    setProvider(KEY_PROVIDERS[next].name)
    radios.current[next]?.focus()
  }

  return (
    <form
      onSubmit={(e) => void submit(e)}
      aria-label="Add your own provider key"
      className={cn(
        'space-y-3 rounded border border-brand-secondary-500/30 bg-brand-secondary-500/[0.06] px-3.5 py-3 text-xs leading-5',
        className,
      )}
    >
      <div className="flex items-start gap-2">
        <Iconify.Icon
          icon="ph:key-bold"
          className="mt-0.5 size-3.5 shrink-0 text-brand-secondary-300"
        />
        <p className="font-medium text-brand-main-50">{title}</p>
      </div>
      <div className="space-y-2 sm:pl-5.5">
        <div
          role="radiogroup"
          aria-label="Provider"
          className="flex flex-wrap items-center gap-1.5"
        >
          {KEY_PROVIDERS.map((p, i) => {
            const selected = p.name === provider
            return (
              <button
                key={p.name}
                ref={(el) => {
                  radios.current[i] = el
                }}
                type="button"
                role="radio"
                aria-checked={selected}
                tabIndex={selected ? 0 : -1}
                onClick={() => setProvider(p.name)}
                onKeyDown={(e) => onRadioKey(e, i)}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded border px-2.5 py-1 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-secondary-400/60',
                  selected
                    ? 'border-brand-secondary-500/60 bg-brand-secondary-500/15 text-brand-secondary-100'
                    : 'border-brand-main-700 bg-brand-main-900 text-brand-main-200 hover:border-brand-main-500 hover:text-brand-main-50',
                )}
              >
                <Iconify.Icon icon={p.icon} className="size-3.5" />
                {p.label}
              </button>
            )
          })}
          {onOtherProvider && (
            <button
              type="button"
              onClick={onOtherProvider}
              className="rounded px-1 text-[11px] text-brand-main-300 underline-offset-2 transition-colors hover:text-brand-main-100 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-secondary-400/60"
            >
              Other providers
            </button>
          )}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            type="password"
            autoComplete="off"
            spellCheck={false}
            autoFocus={autoFocus}
            aria-label="API key"
            aria-describedby={hintId}
            aria-invalid={error ? true : undefined}
            placeholder={active.placeholder}
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            className="h-8 flex-1 border-brand-main-700 bg-brand-main-900 text-xs"
          />
          <Button
            type="submit"
            size="sm"
            disabled={!apiKey.trim() || configure.isPending}
            className="gap-2"
          >
            {configure.isPending ? (
              <Iconify.Icon
                icon="lucide:loader-2"
                className="size-4 animate-spin"
              />
            ) : (
              <Iconify.Icon icon="lucide:check" className="size-4" />
            )}
            {saveLabel}
          </Button>
        </div>
        {error ? (
          <p id={hintId} role="alert" className="text-[11px] text-danger">
            {error}
          </p>
        ) : (
          <p id={hintId} className="text-[11px] text-brand-main-300">
            Your key is used only for requests from this organization.
          </p>
        )}
      </div>
    </form>
  )
}

import { Iconify } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'

type ProviderAsset = {
  type: 'image' | 'icon'
  value: string
  light?: boolean
}

const LIGHT_IMAGE_PROVIDERS = new Set([
  'anthropic',
  'aws',
  'cerebras',
  'groq',
  'ollama',
  'openai',
  'openrouter',
  'moonshot',
  'xai',
  // Coding-agent marks that ship as monochrome black, inverted to white on the
  // dark UI.
  'cursor',
  'github-copilot',
])

// Unified mapping for provider assets - returns either image path or icon name
export function getProviderAsset(providerName: string): ProviderAsset {
  const normalizedProvider = providerName?.trim().toLowerCase()

  // Handle empty, null, undefined, or "N/A" cases
  if (!normalizedProvider || normalizedProvider === 'n/a') {
    return { type: 'icon', value: 'hugeicons:ai-cloud' }
  }

  const assetMap: Record<string, ProviderAsset> = {
    openai: { type: 'image', value: '/providers/lobehub/openai.svg' },
    anthropic: { type: 'image', value: '/providers/lobehub/anthropic.svg' },
    claude: { type: 'image', value: '/providers/lobehub/claude.svg' },
    google: { type: 'image', value: '/providers/lobehub/gemini.svg' },
    azure: { type: 'image', value: '/providers/lobehub/azure.svg' },
    'azure-openai': { type: 'image', value: '/providers/lobehub/azure.svg' },
    aws: { type: 'image', value: '/providers/lobehub/aws.svg' },
    'aws-bedrock': { type: 'image', value: '/providers/lobehub/bedrock.svg' },
    'vertex-ai': { type: 'image', value: '/providers/lobehub/vertexai.svg' },
    groq: { type: 'image', value: '/providers/lobehub/groq.svg' },
    together: { type: 'image', value: '/providers/lobehub/together.svg' },
    fireworks: { type: 'image', value: '/providers/lobehub/fireworks.svg' },
    deepseek: { type: 'image', value: '/providers/lobehub/deepseek.svg' },
    mistral: { type: 'image', value: '/providers/lobehub/mistral.svg' },
    cohere: { type: 'image', value: '/providers/lobehub/cohere.svg' },
    xai: { type: 'image', value: '/providers/lobehub/xai.svg' },
    perplexity: { type: 'image', value: '/providers/lobehub/perplexity.svg' },
    cerebras: { type: 'image', value: '/providers/lobehub/cerebras.svg' },
    'nvidia-nim': { type: 'image', value: '/providers/lobehub/nvidia.svg' },
    openrouter: { type: 'image', value: '/providers/lobehub/openrouter.svg' },
    ollama: { type: 'image', value: '/providers/lobehub/ollama.svg' },
    huggingface: { type: 'image', value: '/providers/lobehub/huggingface.svg' },
    qwen: { type: 'image', value: '/providers/lobehub/qwen.svg' },
    minimax: { type: 'image', value: '/providers/lobehub/minimax.svg' },
    moonshot: { type: 'image', value: '/providers/lobehub/moonshot.svg' },
    zhipu: { type: 'image', value: '/providers/lobehub/zhipu.svg' },
    // Z.AI is Zhipu's international brand and ships the GLM models under the
    // same mark, so both provider ids resolve to the one asset.
    zai: { type: 'image', value: '/providers/lobehub/zhipu.svg' },
    // Coding-agent brand marks (used for the trace-name client logo).
    cursor: { type: 'image', value: '/providers/lobehub/cursor.svg' },
    'github-copilot': { type: 'image', value: '/providers/lobehub/githubcopilot.svg' },
  }

  const asset = assetMap[normalizedProvider] || {
    type: 'icon',
    value: 'hugeicons:ai-cloud',
  }

  if (asset.type === 'image' && LIGHT_IMAGE_PROVIDERS.has(normalizedProvider)) {
    return { ...asset, light: true }
  }

  return asset
}

// Component for provider icons
interface ProviderIconProps {
  providerName: string
  isActive: boolean
  size?: 'sm' | 'md' | 'lg'
}

const sizeClasses = {
  sm: 'size-4',
  md: 'size-5',
  lg: 'size-6',
}

export function ProviderIcon({ providerName, size = 'lg' }: ProviderIconProps) {
  const asset = getProviderAsset(providerName)

  // Only render if it's an icon type
  if (asset.type !== 'icon') {
    return null
  }

  return <Iconify.Icon icon={asset.value} className={cn(sizeClasses[size])} />
}

// Component for provider images
interface ProviderImageProps {
  providerName: string
  isActive: boolean
  size?: 'sm' | 'md' | 'lg'
}

export function ProviderImage({
  providerName,
  isActive,
  size = 'lg',
}: ProviderImageProps) {
  const asset = getProviderAsset(providerName)

  // If it's not an image type, fallback to icon
  if (asset.type !== 'image') {
    return (
      <ProviderIcon
        providerName={providerName}
        isActive={isActive}
        size={size}
      />
    )
  }

  return (
    <div className="relative">
      <img
        src={asset.value}
        alt={`${providerName} logo`}
        className={cn(
          sizeClasses[size],
          'object-contain',
          asset.light && 'brightness-0 invert',
        )}
        onError={(e) => {
          // Hide image and show fallback icon
          const target = e.target as HTMLImageElement
          target.style.display = 'none'
          const fallbackIcon = target.parentElement?.querySelector(
            '.fallback-icon',
          ) as HTMLElement
          if (fallbackIcon) {
            fallbackIcon.style.display = 'block'
          }
        }}
      />
      <Iconify.Icon
        icon="hugeicons:ai-cloud-01"
        className={cn(
          sizeClasses[size],
          'fallback-icon absolute inset-0 hidden',
          isActive ? 'text-brand-secondary-500' : 'text-white/70 light:text-black/70',
        )}
      />
    </div>
  )
}

// Unified component that handles conditional logic internally
interface ProviderDisplayProps {
  providerName: string
  isActive: boolean
  useImage?: boolean
  size?: 'sm' | 'md' | 'lg'
}

export function ProviderDisplay({
  providerName,
  isActive,
  useImage = true,
  size = 'lg',
}: ProviderDisplayProps) {
  const asset = getProviderAsset(providerName)

  // If useImage is false, always use icon
  if (!useImage) {
    return (
      <ProviderIcon
        providerName={providerName}
        isActive={isActive}
        size={size}
      />
    )
  }

  // If the asset is an image type, use the image component
  if (asset.type === 'image') {
    return (
      <ProviderImage
        providerName={providerName}
        isActive={isActive}
        size={size}
      />
    )
  }

  // Otherwise use icon
  return (
    <ProviderIcon providerName={providerName} isActive={isActive} size={size} />
  )
}

// Shared provider name formatter — use everywhere provider names are displayed
export function formatProviderName(name: string): string {
  const map: Record<string, string> = {
    openai: 'OpenAI',
    anthropic: 'Anthropic',
    google: 'Google',
    cohere: 'Cohere',
    'azure-openai': 'Azure OpenAI',
    'aws-bedrock': 'AWS Bedrock',
    'vertex-ai': 'Vertex AI',
    huggingface: 'HuggingFace',
    ollama: 'Ollama',
    deepseek: 'DeepSeek',
    groq: 'Groq',
    together: 'Together AI',
    fireworks: 'Fireworks',
    xai: 'xAI',
    perplexity: 'Perplexity',
    cerebras: 'Cerebras',
    mistral: 'Mistral',
    'nvidia-nim': 'NVIDIA NIM',
    minimax: 'MiniMax',
    moonshot: 'Moonshot',
    qwen: 'Qwen',
    openrouter: 'OpenRouter',
  }
  return map[name.toLowerCase()] ?? name
}

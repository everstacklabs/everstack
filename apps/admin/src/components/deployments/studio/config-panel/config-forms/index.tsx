import type { StudioNodeType, NodeConfig } from '../../types'
import { StartConfigForm } from './start-config'
import { AuthConfigForm } from './auth-config'
import { RateLimiterConfigForm } from './rate-limiter-config'
import { CacheConfigForm } from './cache-config'
import { RouterConfigForm } from './router-config'
import { LoadBalancerConfigForm } from './load-balancer-config'
import { InputGuardrailsConfigForm } from './input-guardrails-config'
import { OutputGuardrailsConfigForm } from './output-guardrails-config'
import { ProviderConfigForm } from './provider-config'
import { FunctionConfigForm } from './function-config'
import { HttpRequestConfigForm } from './http-request-config'
import { WebhookConfigForm } from './webhook-config'
import { IfElseConfigForm } from './if-else-config'
import { ResponseConfigForm } from './response-config'
import { AgentConfigForm } from './agent-config-form'
import { MemoryConfigForm } from './memory-config-form'
import { TTSConfigForm } from './tts-config'
import { STTConfigForm } from './stt-config'
import { VoiceCloneConfigForm } from './voice-clone-config'

interface ConfigFormProps {
    nodeType: StudioNodeType
    config: NodeConfig
    onChange: (config: NodeConfig) => void
}

export function ConfigFormForType({ nodeType, config, onChange }: ConfigFormProps) {
    switch (nodeType) {
        case 'start':
            return <StartConfigForm config={config as any} onChange={onChange as any} />
        case 'auth':
            return <AuthConfigForm config={config as any} onChange={onChange as any} />
        case 'rateLimiter':
            return <RateLimiterConfigForm />
        case 'cache':
            return <CacheConfigForm config={config as any} onChange={onChange as any} />
        case 'router':
            return <RouterConfigForm config={config as any} onChange={onChange as any} />
        case 'loadBalancer':
            return <LoadBalancerConfigForm config={config as any} onChange={onChange as any} />
        case 'inputGuardrails':
            return <InputGuardrailsConfigForm config={config as any} onChange={onChange as any} />
        case 'outputGuardrails':
            return <OutputGuardrailsConfigForm config={config as any} onChange={onChange as any} />
        case 'provider':
            return <ProviderConfigForm config={config as any} onChange={onChange as any} />
        case 'function':
            return <FunctionConfigForm config={config as any} onChange={onChange as any} />
        case 'httpRequest':
            return <HttpRequestConfigForm config={config as any} onChange={onChange as any} />
        case 'webhook':
            return <WebhookConfigForm config={config as any} onChange={onChange as any} />
        case 'ifElse':
            return <IfElseConfigForm config={config as any} onChange={onChange as any} />
        case 'agent':
            return <AgentConfigForm config={config as any} onChange={onChange as any} />
        case 'memory':
            return <MemoryConfigForm config={config as any} onChange={onChange as any} />
        case 'response':
            return <ResponseConfigForm config={config as any} onChange={onChange as any} />
        case 'tts':
            return <TTSConfigForm config={config as any} onChange={onChange as any} />
        case 'stt':
            return <STTConfigForm config={config as any} onChange={onChange as any} />
        case 'voiceClone':
            return <VoiceCloneConfigForm config={config as any} onChange={onChange as any} />
        default:
            return <div className="text-sm text-brand-main-400">No configuration available</div>
    }
}

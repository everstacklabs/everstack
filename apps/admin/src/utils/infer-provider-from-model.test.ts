import { describe, it, expect } from 'vitest'
import { inferProviderFromModel } from './infer-provider-from-model'

describe('inferProviderFromModel', () => {
    it('infers the provider from common model prefixes', () => {
        const cases: Record<string, string> = {
            'claude-sonnet-4-6': 'anthropic',
            'claude-3-5-haiku': 'anthropic',
            'gpt-4o': 'openai',
            'o3-mini': 'openai',
            'gemini-2.0-flash': 'google',
            'mistral-large': 'mistral',
            'command-r-plus': 'cohere',
            'deepseek-chat': 'deepseek',
            'qwen2.5-72b': 'qwen',
            'grok-2': 'xai',
        }
        for (const [model, provider] of Object.entries(cases)) {
            expect(inferProviderFromModel(model)).toBe(provider)
        }
    })

    it('uses an explicit provider/ prefix', () => {
        expect(inferProviderFromModel('anthropic/claude-3')).toBe('anthropic')
        expect(inferProviderFromModel('openrouter/some-model')).toBe('openrouter')
    })

    it('returns empty for unknown or missing models', () => {
        expect(inferProviderFromModel('')).toBe('')
        expect(inferProviderFromModel(undefined)).toBe('')
        expect(inferProviderFromModel('totally-unknown-model')).toBe('')
    })
})

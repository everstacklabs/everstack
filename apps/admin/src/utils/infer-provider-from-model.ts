// Infer a provider from a model id when a trace carries no explicit provider
// (e.g. external coding-agent telemetry like Claude Code, which records a model
// but no Everstack provider). Lets the traces table show a real logo instead of
// a generic placeholder. Returns '' when nothing matches.

const PREFIX_RULES: Array<{ test: (m: string) => boolean; provider: string }> = [
    { test: (m) => m.startsWith('claude'), provider: 'anthropic' },
    { test: (m) => m.startsWith('gpt') || m.startsWith('o1') || m.startsWith('o3') || m.startsWith('o4') || m.startsWith('chatgpt') || m.startsWith('text-') || m.startsWith('davinci'), provider: 'openai' },
    { test: (m) => m.startsWith('gemini') || m.startsWith('palm') || m.startsWith('bison'), provider: 'google' },
    { test: (m) => m.startsWith('mistral') || m.startsWith('mixtral') || m.startsWith('ministral') || m.startsWith('magistral'), provider: 'mistral' },
    { test: (m) => m.startsWith('command') || m.startsWith('cohere'), provider: 'cohere' },
    { test: (m) => m.startsWith('deepseek'), provider: 'deepseek' },
    { test: (m) => m.startsWith('qwen'), provider: 'qwen' },
    { test: (m) => m.startsWith('grok'), provider: 'xai' },
    { test: (m) => m.startsWith('moonshot') || m.startsWith('kimi'), provider: 'moonshot' },
    { test: (m) => m.startsWith('minimax') || m.startsWith('abab'), provider: 'minimax' },
    { test: (m) => m.startsWith('sonar'), provider: 'perplexity' },
]

export function inferProviderFromModel(model: string | undefined): string {
    if (!model) return ''
    // Strip a leading "provider/" if the model id carries one (e.g. "anthropic/claude-...").
    const slash = model.indexOf('/')
    const bare = (slash >= 0 ? model.slice(slash + 1) : model).trim().toLowerCase()
    if (slash >= 0) {
        // The prefix itself may already be a provider name.
        const prefix = model.slice(0, slash).trim().toLowerCase()
        if (prefix) return prefix
    }
    for (const rule of PREFIX_RULES) {
        if (rule.test(bare)) return rule.provider
    }
    return ''
}

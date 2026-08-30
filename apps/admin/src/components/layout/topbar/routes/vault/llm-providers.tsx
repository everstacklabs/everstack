import { type ActionGroup } from '@/components/layout/topbar/types'

export const VaultLlmProvidersActions: ActionGroup[] = [
  {
    title: 'LLM Providers',
    actions: [
      // Search actions
      {
        type: 'search',
        key: 'search-llm-providers',
        label: 'Search',
        searchParam: 'search',
        placeholder: 'OpenAI, Anthropic, etc...',
        debounceMs: 300,
      },
      // Filter actions
      {
        type: 'filter',
        key: 'filter-llm-provider-type',
        label: 'Type',
        filterType: 'select',
        storeKey: 'llm-provider-type',
        storeAction: 'setType',
        options: [
          { value: 'all', label: 'All Types' },
          { value: 'openai', label: 'OpenAI' },
          { value: 'anthropic', label: 'Anthropic' },
          { value: 'azure-openai', label: 'Azure OpenAI' },
          { value: 'aws-bedrock', label: 'AWS Bedrock' },
          { value: 'vertex-ai', label: 'Vertex AI' },
          { value: 'groq', label: 'Groq' },
          { value: 'together', label: 'Together AI' },
          { value: 'fireworks', label: 'Fireworks AI' },
          { value: 'xai', label: 'xAI' },
          { value: 'perplexity', label: 'Perplexity' },
          { value: 'cerebras', label: 'Cerebras' },
          { value: 'nvidia-nim', label: 'NVIDIA NIM' },
        ],
      },
      {
        type: 'filter',
        key: 'filter-llm-provider-status',
        label: 'Status',
        filterType: 'select',
        storeKey: 'llm-provider-status',
        storeAction: 'setStatus',
        options: [
          { value: 'all', label: 'All Status' },
          { value: 'active', label: 'Active' },
          { value: 'expired', label: 'Expired' },
        ],
      },
    ],
  },
]

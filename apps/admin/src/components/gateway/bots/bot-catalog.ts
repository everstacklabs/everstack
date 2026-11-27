// Setup instructions for each kind of personal bot, keyed by the surfaces a bot
// key can use. Every config block below was checked against the bot's source or
// official docs on 2026-11-09, and its keys are copied exactly. Where a setting
// name could not be confirmed, the entry lists plain values instead of a guessed
// config. Templates use {{GATEWAY_URL}}, {{API_KEY}}, {{MODEL}} and {{AGENT_ID}};
// renderSetup replaces all of them.

export type Surface = 'inference' | 'mcp' | 'telemetry' | 'a2a'

export const SURFACES: Surface[] = ['inference', 'mcp', 'telemetry', 'a2a']

export const SURFACE_INFO: Record<Surface, { label: string; description: string }> = {
    inference: { label: 'Models', description: 'Use this gateway as its model provider.' },
    mcp: { label: 'Tools', description: 'Call the tools this gateway exposes over MCP.' },
    telemetry: { label: 'Traces', description: 'Send its traces and logs to this gateway.' },
    a2a: { label: 'Agents', description: 'Call your published Everstack agents over A2A.' },
}

export type BotKind =
    | 'openclaw'
    | 'hermes'
    | 'nanobot'
    | 'zeroclaw'
    | 'nanoclaw'
    | 'copilotkit-openbot'
    | 'nightly-openbot'
    | 'grok-bot'
    | 'grok-build'
    | 'meta-muse'
    | 'custom'

/** The surfaces the gateway accepts for each kind (internal/bots/http.go
 *  kindSurfaces). The catalog may offer fewer, never more. */
export const BACKEND_SURFACES: Record<BotKind, Surface[]> = {
    openclaw: ['inference', 'mcp', 'telemetry', 'a2a'],
    hermes: ['inference', 'mcp', 'telemetry', 'a2a'],
    nanobot: ['inference', 'mcp', 'telemetry', 'a2a'],
    zeroclaw: ['inference', 'mcp', 'telemetry', 'a2a'],
    nanoclaw: ['inference', 'mcp', 'telemetry', 'a2a'],
    'copilotkit-openbot': ['inference', 'mcp', 'telemetry', 'a2a'],
    'nightly-openbot': ['inference', 'mcp', 'telemetry', 'a2a'],
    custom: ['inference', 'mcp', 'telemetry', 'a2a'],
    'grok-bot': ['mcp'],
    'grok-build': ['inference', 'mcp'],
    'meta-muse': ['mcp'],
}

/** A gateway capability a block relies on beyond the bot key itself. */
export type GatewayNeed = 'anthropic-messages' | 'a2a-1.0'

export type Block =
    | {
          type: 'code'
          title: string
          language: 'json' | 'json5' | 'yaml' | 'toml' | 'bash'
          code: string
          caveats?: string[]
          needs?: GatewayNeed
          /** Covered by the kind's single-file config when that is shown. */
          merged?: boolean
      }
    | {
          type: 'values'
          title: string
          /** Where the values go, e.g. "provider" or "MCP server". */
          settings: string
          values: { label: string; value: string }[]
          caveats?: string[]
          needs?: GatewayNeed
      }
    | {
          type: 'steps'
          title: string
          steps: string[]
          caveats?: string[]
      }
    /** Caveats for a surface that has no block of its own. */
    | { type: 'note'; caveats: string[] }

export interface KindEntry {
    id: BotKind
    name: string
    description: string
    hosted?: 'xAI' | 'Meta'
    /** Where the settings live. Inline code is wrapped in backticks. */
    configLocation: string
    /** How changes take effect. */
    apply: string
    /** The surfaces this bot can use, with the blocks for each. */
    surfaces: Partial<Record<Surface, Block[]>>
    /** Whether the bot can serve A2A, so Everstack agents can call it. */
    exposesA2a: boolean
    /** One file holding every enabled surface, when the bot reads one file. */
    merged?: (enabled: ReadonlySet<Surface>) => { title: string; language: 'json' | 'json5' | 'yaml' | 'toml'; code: string; note?: string } | null
}

const BEARER = 'Bearer {{API_KEY}}'

// ─── OpenClaw ───────────────────────────────────────────────────────

const openclawParts = {
    inference: `  models: {
    providers: {
      everstack: {
        baseUrl: "{{GATEWAY_URL}}/v1",
        apiKey: "{{API_KEY}}",
        api: "openai-completions",
        models: [{ id: "{{MODEL}}", name: "{{MODEL}}", input: ["text"], contextWindow: 128000, maxTokens: 8192 }],
      },
    },
  },
  agents: { defaults: { model: { primary: "everstack/{{MODEL}}" } } },`,
    mcp: `  mcp: {
    servers: {
      everstack: {
        url: "{{GATEWAY_URL}}/mcp",
        transport: "streamable-http",
        headers: { Authorization: "Bearer {{API_KEY}}" },
      },
    },
  },`,
    telemetry: `  plugins: { entries: { "diagnostics-otel": { enabled: true } } },
  diagnostics: {
    enabled: true,
    otel: {
      enabled: true,
      endpoint: "{{GATEWAY_URL}}",
      protocol: "http/protobuf",
      headers: { Authorization: "Bearer {{API_KEY}}" },
      traces: true,
      metrics: true,
      logs: false,
    },
  },`,
    a2a: `  channels: {
    a2a: {
      enabled: true,
      peers: {
        everstack: {
          token: "<a secret of your choice>", // required by the schema even for outbound-only peers
          url: "{{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}}",
          outboundToken: "{{API_KEY}}",
        },
      },
    },
  },`,
}

const openclaw: KindEntry = {
    id: 'openclaw',
    name: 'OpenClaw',
    description: 'Self-hosted personal assistant with chat channels, tools and plugins.',
    configLocation: '`~/.openclaw/openclaw.json` (JSON5)',
    apply: 'OpenClaw watches the file and applies most changes on its own. Restart it after changing `channels.a2a`. After a rejected key it pauses the provider for about a minute, so a new key can take that long to start working. Restart OpenClaw after a new key: models and tools pick it up on their own, but trace export can stay off until a restart.',
    exposesA2a: true,
    merged: (enabled) => {
        const parts = SURFACES.filter((s) => enabled.has(s)).map((s) => openclawParts[s])
        if (parts.length < 2) return null
        return {
            title: 'openclaw.json',
            language: 'json5',
            code: `{\n${parts.join('\n')}\n}`,
            // Without the plugin the file validates and no trace is exported, so
            // say it here, next to the file people paste and stop at.
            note: enabled.has('telemetry')
                ? 'Merge these keys into your existing file. For traces, first run: openclaw plugins install @openclaw/diagnostics-otel'
                : 'Merge these keys into your existing file.',
        }
    },
    surfaces: {
        inference: [
            {
                type: 'code',
                title: 'OpenAI-compatible provider',
                language: 'json5',
                merged: true,
                code: `{
  models: {
    providers: {
      everstack: {
        baseUrl: "{{GATEWAY_URL}}/v1",
        apiKey: "{{API_KEY}}",
        api: "openai-completions", // or "openai-responses" for /v1/responses
        models: [
          { id: "{{MODEL}}", name: "{{MODEL}}", input: ["text"], contextWindow: 128000, maxTokens: 8192 },
        ],
      },
    },
  },
  agents: { defaults: { model: { primary: "everstack/{{MODEL}}" } } },
}`,
                caveats: ['Set `contextWindow` and `maxTokens` to the real limits of your model; the numbers shown are editable defaults.'],
            },
            {
                type: 'code',
                title: 'Anthropic-compatible provider (alternative)',
                language: 'json5',
                needs: 'anthropic-messages',
                code: `{
  models: {
    providers: {
      "everstack-anthropic": {
        baseUrl: "{{GATEWAY_URL}}", // no /v1: the Anthropic client appends it
        apiKey: "{{API_KEY}}",
        api: "anthropic-messages",
        models: [{ id: "{{MODEL}}", name: "{{MODEL}}", input: ["text"], contextWindow: 200000, maxTokens: 8192 }],
      },
    },
  },
  agents: { defaults: { model: { primary: "everstack-anthropic/{{MODEL}}" } } },
}`,
            },
        ],
        mcp: [
            {
                type: 'code',
                title: 'MCP server',
                language: 'json5',
                merged: true,
                code: `{
  mcp: {
    servers: {
      everstack: {
        url: "{{GATEWAY_URL}}/mcp",
        transport: "streamable-http",
        headers: { Authorization: "Bearer {{API_KEY}}" },
      },
    },
  },
}`,
                caveats: [
                    'Keep `transport: "streamable-http"`: without it OpenClaw uses SSE, which the gateway does not serve.',
                    'Do not set `auth: "oauth"`, or OpenClaw ignores the Authorization header.',
                ],
            },
            {
                type: 'code',
                title: 'Or add it with the CLI',
                language: 'bash',
                code: `openclaw mcp set everstack '{"url":"{{GATEWAY_URL}}/mcp","transport":"streamable-http","headers":{"Authorization":"Bearer {{API_KEY}}"}}'
openclaw mcp doctor --probe`,
            },
        ],
        telemetry: [
            {
                type: 'code',
                title: 'Install the telemetry plugin',
                language: 'bash',
                code: `openclaw plugins install @openclaw/diagnostics-otel`,
                caveats: ['Without the plugin OpenClaw accepts the config but exports nothing.'],
            },
            {
                type: 'code',
                title: 'OpenTelemetry export',
                language: 'json5',
                merged: true,
                code: `{
  plugins: {
    entries: { "diagnostics-otel": { enabled: true } },
  },
  diagnostics: {
    enabled: true,
    otel: {
      enabled: true,
      endpoint: "{{GATEWAY_URL}}", // OpenClaw appends /v1/traces, /v1/metrics, /v1/logs
      protocol: "http/protobuf",
      serviceName: "openclaw",
      headers: { Authorization: "Bearer {{API_KEY}}" },
      traces: true,
      metrics: true,
      logs: false,
    },
  },
}`,
                caveats: [
                    '`diagnostics.otel.endpoint` is a base URL: OpenClaw appends `/v1/traces`, `/v1/metrics` and `/v1/logs` to it.',
                    'If your config already has `plugins.allow`, add `"diagnostics-otel"` to that list.',
                ],
            },
        ],
        a2a: [
            {
                type: 'code',
                title: 'A2A peer for an Everstack agent',
                language: 'json5',
                merged: true,
                code: `{
  channels: {
    a2a: {
      enabled: true,
      peers: {
        everstack: {
          token: "<a secret of your choice>", // required by the schema even for outbound-only peers
          url: "{{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}}",
          outboundToken: "{{API_KEY}}",
        },
      },
    },
  },
}`,
                caveats: [
                    'Address messages to `a2a:everstack`.',
                    'OpenClaw gives up on an outbound A2A call after 30 seconds, so longer agent runs fail on its side.',
                ],
            },
        ],
    },
}

// ─── Hermes Agent ───────────────────────────────────────────────────

const hermesParts = {
    inference: `providers:
  everstack:
    api: "{{GATEWAY_URL}}/v1"
    api_key: "{{API_KEY}}"
    transport: chat_completions
model:
  provider: custom:everstack
  default: "{{MODEL}}"`,
    mcp: `mcp_servers:
  everstack:
    url: "{{GATEWAY_URL}}/mcp"
    headers:
      Authorization: "Bearer {{API_KEY}}"
    enabled: true`,
    a2a: `a2a_agents:
  everstack:
    url: "{{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}}"
    auth:
      type: bearer
      token: "{{API_KEY}}"
    timeout: 120`,
}

const hermes: KindEntry = {
    id: 'hermes',
    name: 'Hermes Agent',
    description: 'Self-hosted agent from Nous Research with tools and messaging platforms.',
    configLocation: '`~/.hermes/config.yaml`, with secrets optionally in `~/.hermes/.env`',
    apply: 'Run `/reload-mcp` for MCP changes (a running gateway also picks them up within about a minute). Reload or restart Hermes after provider changes.',
    exposesA2a: true,
    merged: (enabled) => {
        const keys = (['inference', 'mcp', 'a2a'] as const).filter((s) => enabled.has(s))
        if (keys.length < 2) return null
        return {
            title: 'config.yaml',
            language: 'yaml',
            code: keys.map((s) => hermesParts[s]).join('\n'),
            note: 'Merge these keys into your existing file.',
        }
    },
    surfaces: {
        inference: [
            {
                type: 'code',
                title: 'OpenAI-compatible provider',
                language: 'yaml',
                merged: true,
                code: `# ~/.hermes/config.yaml
providers:
  everstack:
    api: "{{GATEWAY_URL}}/v1"
    api_key: "{{API_KEY}}"
    transport: chat_completions   # or codex_responses for /v1/responses
model:
  provider: custom:everstack
  default: "{{MODEL}}"`,
                caveats: ['Keep the quotes around the values.'],
            },
            {
                type: 'code',
                title: 'Anthropic-compatible provider (alternative)',
                language: 'yaml',
                needs: 'anthropic-messages',
                code: `providers:
  everstack-anthropic:
    api: "{{GATEWAY_URL}}"            # a trailing /v1 is stripped anyway
    api_key: "{{API_KEY}}"            # sent as x-api-key for non-anthropic.com hosts
    transport: anthropic_messages
model:
  provider: custom:everstack-anthropic
  default: "{{MODEL}}"`,
            },
        ],
        mcp: [
            {
                type: 'code',
                title: 'MCP server',
                language: 'yaml',
                merged: true,
                code: `mcp_servers:
  everstack:
    url: "{{GATEWAY_URL}}/mcp"
    headers:
      Authorization: "Bearer {{API_KEY}}"
    enabled: true
    timeout: 300
    connect_timeout: 60`,
                caveats: ['Leave `transport` unset: a bare `url` uses Streamable HTTP.'],
            },
        ],
        telemetry: [
            {
                type: 'code',
                title: 'Install the hermes_otel plugin',
                language: 'bash',
                code: `hermes plugins install hermes_otel
hermes plugins enable hermes_otel`,
                caveats: [
                    'Hermes itself exports only health data; run traces need the community `hermes_otel` plugin.',
                    'Restart Hermes after enabling it (`hermes gateway restart` when the gateway is running).',
                ],
            },
            {
                type: 'code',
                title: '~/.hermes/hermes_otel.yaml',
                language: 'yaml',
                code: `# ~/.hermes/hermes_otel.yaml
backends:
  - type: otlp
    name: everstack
    endpoint: "{{GATEWAY_URL}}/v1/traces"
    headers:
      Authorization: "Bearer {{API_KEY}}"`,
                caveats: ['`endpoint` is the full traces URL.'],
            },
        ],
        a2a: [
            {
                type: 'code',
                title: 'A2A agent',
                language: 'yaml',
                merged: true,
                needs: 'a2a-1.0',
                code: `a2a_agents:
  everstack:
    url: "{{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}}"
    auth:
      type: bearer
      token: "{{API_KEY}}"
    timeout: 120`,
                caveats: ['Hermes calls agents with A2A 1.0 (`SendMessage` and the `A2A-Version: 1.0` header).'],
            },
            {
                type: 'code',
                title: 'Enable the A2A toolset',
                language: 'bash',
                code: 'hermes tools enable a2a --platform <platform>',
            },
        ],
    },
}

// ─── nanobot ────────────────────────────────────────────────────────

const nanobotParts = {
    inference: {
        providers: {
            everstack: {
                apiBase: '{{GATEWAY_URL}}/v1',
                apiKey: '{{API_KEY}}',
                api: { supportedApis: ['chat_completions'], preferredApi: 'chat_completions' },
            },
        },
        modelPresets: {
            everstack: { provider: 'everstack', model: '{{MODEL}}' },
        },
        agents: { defaults: { modelPreset: 'everstack' } },
    },
    mcp: {
        tools: {
            mcpServers: {
                everstack: {
                    type: 'streamableHttp',
                    url: '{{GATEWAY_URL}}/mcp',
                    headers: { Authorization: BEARER },
                    toolTimeout: 120,
                },
            },
        },
    },
}

const nanobot: KindEntry = {
    id: 'nanobot',
    name: 'nanobot',
    description: 'Lightweight personal agent from HKUDS with chat channels and MCP tools.',
    configLocation: '`~/.nanobot/config.json`',
    apply: 'Model changes apply on the next request. Restart nanobot after MCP changes.',
    exposesA2a: false,
    merged: (enabled) => {
        if (!enabled.has('inference') || !enabled.has('mcp')) return null
        return {
            title: 'config.json',
            language: 'json',
            code: JSON.stringify({ ...nanobotParts.inference, ...nanobotParts.mcp }, null, 2),
            note: 'Merge these keys into your existing config.json rather than replacing it.',
        }
    },
    surfaces: {
        inference: [
            {
                type: 'code',
                title: 'OpenAI-compatible provider',
                language: 'json',
                merged: true,
                code: JSON.stringify(nanobotParts.inference, null, 2),
                caveats: ['For `/v1/responses`, set `supportedApis` to `["responses"]` and `preferredApi` to `"responses"`.'],
            },
            {
                type: 'code',
                title: 'Anthropic-compatible provider (alternative)',
                language: 'json',
                needs: 'anthropic-messages',
                code: JSON.stringify(
                    {
                        providers: {
                            everstackAnthropic: {
                                apiBase: '{{GATEWAY_URL}}',
                                apiKey: '{{API_KEY}}',
                                api: { supportedApis: ['anthropic_messages'], preferredApi: 'anthropic_messages' },
                            },
                        },
                        modelPresets: {
                            everstackClaude: { provider: 'everstackAnthropic', model: '{{MODEL}}' },
                        },
                        agents: { defaults: { modelPreset: 'everstackClaude' } },
                    },
                    null,
                    2,
                ),
                caveats: ['A model preset cannot mix `anthropic_messages` with the OpenAI APIs.'],
            },
        ],
        mcp: [
            {
                type: 'code',
                title: 'MCP server',
                language: 'json',
                merged: true,
                code: JSON.stringify(nanobotParts.mcp, null, 2),
                caveats: ['nanobot blocks private and loopback hosts unless their range is listed, for example `"ssrfWhitelist": ["10.0.0.0/8"]` under `tools`.'],
            },
        ],
    },
}

// ─── ZeroClaw ───────────────────────────────────────────────────────

function zeroclawConfig(enabled: ReadonlySet<Surface>): string {
    const out: string[] = ['schema_version = 4', '']
    if (enabled.has('inference')) {
        out.push(
            '[providers.models.custom.everstack]',
            'uri = "{{GATEWAY_URL}}/v1"',
            'api_key = "{{API_KEY}}"',
            'model = "{{MODEL}}"',
            '',
        )
    }
    if (enabled.has('inference') || enabled.has('mcp')) {
        out.push('[agents.default]')
        if (enabled.has('inference')) {
            out.push('enabled = true', 'model_provider = "custom.everstack"', 'risk_profile = "default"')
        } else {
            out.push('# keep your existing model_provider and risk_profile here')
        }
        if (enabled.has('mcp')) out.push('mcp_bundles = ["everstack"]')
        out.push('')
        if (enabled.has('inference')) out.push('[risk_profiles.default]', '')
    }
    if (enabled.has('mcp')) {
        out.push(
            '[[mcp.servers]]',
            'name = "everstack"',
            'transport = "http"',
            'url = "{{GATEWAY_URL}}/mcp"',
            'headers = { Authorization = "Bearer {{API_KEY}}" }',
            '',
            '[mcp_bundles.everstack]',
            'servers = ["everstack"]',
            '',
        )
    }
    if (enabled.has('telemetry')) {
        out.push(
            '[observability]',
            'backend = "otel"',
            'otel_endpoint = "{{GATEWAY_URL}}"',
            'otel_service_name = "zeroclaw"',
            '',
            '[observability.otel_headers]',
            'Authorization = "Bearer {{API_KEY}}"',
            '',
        )
    }
    if (enabled.has('a2a')) {
        out.push(
            '[a2a.client]',
            'enabled = true',
            '',
            '[[a2a.client.peers]]',
            'name = "everstack"',
            'base_url = "{{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}}"',
            'token = "{{API_KEY}}"',
            '',
        )
    }
    return out.join('\n').trimEnd()
}

const zeroclaw: KindEntry = {
    id: 'zeroclaw',
    name: 'ZeroClaw',
    description: 'Rust agent runtime with providers, MCP bundles and A2A.',
    configLocation: '`~/.zeroclaw/config.toml` (or the directory in `ZEROCLAW_CONFIG_DIR`)',
    apply: 'Reload with `/admin/reload` or restart the daemon. A standalone `zeroclaw gateway start` needs a restart, and MCP grant changes need a new session.',
    exposesA2a: true,
    // TOML rejects a repeated [agents.default] table, so ZeroClaw always gets
    // one generated file instead of separate parts.
    merged: (enabled) => {
        if (enabled.size === 0) return null
        return {
            title: 'config.toml',
            language: 'toml',
            code: zeroclawConfig(enabled),
            note: 'Merge into your existing file; TOML allows each table header only once.',
        }
    },
    surfaces: {
        inference: [
            {
                type: 'note',
                caveats: ['The agent\'s `model_provider` picks the provider, and the model comes from that provider entry.'],
            },
            {
                type: 'code',
                title: 'Anthropic-compatible provider (alternative)',
                language: 'toml',
                needs: 'anthropic-messages',
                code: `[providers.models.anthropic.everstack]
uri = "{{GATEWAY_URL}}"
api_key = "{{API_KEY}}"
model = "{{MODEL}}"`,
                caveats: ['To use it, set `model_provider = "anthropic.everstack"` in `[agents.default]`.'],
            },
        ],
        mcp: [
            {
                type: 'note',
                caveats: ['The `mcp_bundles` grant is required: an agent without it connects to no MCP server.'],
            },
        ],
        telemetry: [
            {
                type: 'note',
                caveats: [
                    'Needs a ZeroClaw build with the `observability-otel` feature (or the `all-features` image); without it, export is silently off.',
                    '`otel_endpoint` is a base URL: ZeroClaw appends `/v1/traces`, `/v1/metrics` and `/v1/logs`.',
                ],
            },
        ],
        a2a: [
            {
                type: 'note',
                caveats: ['ZeroClaw calls agents with A2A 1.0 only and reads the agent card at the peer URL first.'],
            },
        ],
    },
}

// ─── NanoClaw ───────────────────────────────────────────────────────

const nanoclaw: KindEntry = {
    id: 'nanoclaw',
    name: 'NanoClaw',
    description: 'Claude Agent SDK assistant that runs each group in its own container.',
    configLocation: 'Project-root `.env` for host settings; group settings through the `ncl` CLI',
    apply: 'Apply group changes with `ncl groups restart --id <group id>`.',
    exposesA2a: false,
    surfaces: {
        inference: [
            {
                type: 'values',
                title: 'Anthropic-compatible provider',
                settings: 'provider',
                needs: 'anthropic-messages',
                values: [
                    { label: 'Base URL', value: '{{GATEWAY_URL}}' },
                    { label: 'Auth token', value: '{{API_KEY}}' },
                    { label: 'Model', value: '{{MODEL}}' },
                ],
                caveats: ['NanoClaw sends the key as a Bearer token (`Authorization: Bearer`), not as `x-api-key`.'],
            },
            {
                type: 'values',
                title: 'OpenAI-compatible provider (with the add-opencode skill)',
                settings: 'provider',
                values: [
                    { label: 'Base URL', value: '{{GATEWAY_URL}}/v1' },
                    { label: 'API key', value: '{{API_KEY}}' },
                    { label: 'Model', value: '{{MODEL}}' },
                ],
                caveats: ['Core NanoClaw is Anthropic-only; this path needs the `/add-opencode` skill.'],
            },
        ],
        mcp: [
            {
                type: 'code',
                title: 'MCP server for a group',
                language: 'bash',
                code: `ncl groups config add-mcp-server --id <group id> --name everstack \\
  --url {{GATEWAY_URL}}/mcp \\
  --headers '{"Authorization":"Bearer {{API_KEY}}"}'
ncl groups restart --id <group id>`,
                caveats: [
                    'NanoClaw accepts only https:// MCP URLs.',
                    'The header value is stored in the group\'s `container.json` inside the agent container.',
                ],
            },
        ],
    },
}

// ─── CopilotKit OpenBot ─────────────────────────────────────────────

const copilotkit: KindEntry = {
    id: 'copilotkit-openbot',
    name: 'CopilotKit OpenBot',
    description: 'Self-hosted team bot platform from CopilotKit with an admin web app.',
    configLocation: 'The deployment `.env` file; MCP servers in the web app at `/admin/plugins`',
    apply: 'Restart the server or containers after editing `.env`.',
    exposesA2a: false,
    surfaces: {
        inference: [
            {
                type: 'code',
                title: 'OpenAI-compatible provider',
                language: 'bash',
                code: `# .env
OPENAI_BASE_URL={{GATEWAY_URL}}/v1
OPENAI_API_KEY={{API_KEY}}
BOT_MODEL={{MODEL}}
# COPILOTKIT_OPENAI_API=chat   # force /chat/completions for every model; or "responses"`,
                caveats: ['OpenBot picks Chat Completions or Responses by model name; set `COPILOTKIT_OPENAI_API` to force one.'],
            },
            {
                type: 'values',
                title: 'Anthropic-compatible provider (alternative)',
                settings: 'provider',
                needs: 'anthropic-messages',
                values: [
                    { label: 'Provider', value: 'anthropic' },
                    { label: 'Base URL', value: '{{GATEWAY_URL}}' },
                    { label: 'API key', value: '{{API_KEY}}' },
                    { label: 'Model', value: '{{MODEL}}' },
                ],
            },
        ],
        mcp: [
            {
                type: 'values',
                title: 'Custom MCP server (Admin, Plugins)',
                settings: 'MCP server',
                values: [
                    { label: 'URL', value: '{{GATEWAY_URL}}/mcp' },
                    { label: 'Token', value: '{{API_KEY}}' },
                ],
                caveats: [
                    'OpenBot sends a bare token as `Authorization: Bearer <token>`.',
                    'Tools from custom MCP servers count as writes by default, so each call goes through OpenBot\'s approval policy.',
                ],
            },
        ],
        telemetry: [
            {
                type: 'values',
                title: 'OTLP export',
                settings: 'OpenTelemetry',
                values: [
                    { label: 'OTLP base URL', value: '{{GATEWAY_URL}}' },
                    { label: 'Header', value: 'Authorization: Bearer {{API_KEY}}' },
                    { label: 'Service name', value: 'openbot' },
                ],
                caveats: ['OpenBot exports audit logs only (to `/v1/logs`), not traces.'],
            },
        ],
    },
}

// ─── nightly-labs openbot ───────────────────────────────────────────

const nightly: KindEntry = {
    id: 'nightly-openbot',
    name: 'openbot (nightly-labs)',
    description: 'Desktop app that runs agent CLIs such as Codex, Claude and Hermes.',
    configLocation: 'The desktop app settings; there is no config file for these',
    apply: 'Enter the values in the app.',
    exposesA2a: false,
    surfaces: {
        inference: [
            {
                type: 'values',
                title: 'Custom OpenAI-compatible endpoint (Providers)',
                settings: 'provider',
                values: [
                    { label: 'Base URL', value: '{{GATEWAY_URL}}/v1' },
                    { label: 'API key', value: '{{API_KEY}}' },
                    { label: 'Model', value: '{{MODEL}}' },
                ],
            },
        ],
        mcp: [
            {
                type: 'values',
                title: 'MCP server (Settings)',
                settings: 'MCP server',
                values: [
                    { label: 'Transport', value: 'http' },
                    { label: 'URL', value: '{{GATEWAY_URL}}/mcp' },
                    { label: 'Header', value: 'Authorization: Bearer {{API_KEY}}' },
                ],
            },
        ],
    },
}

// ─── Hosted bots ────────────────────────────────────────────────────

const grokBot: KindEntry = {
    id: 'grok-bot',
    name: 'Grok Bot',
    description: 'xAI assistant that runs in the cloud with its own computer and apps.',
    hosted: 'xAI',
    configLocation: 'The Grok Bot app; there is no config file',
    apply: 'Grok Bot adds the server when you ask it to.',
    exposesA2a: false,
    surfaces: {
        mcp: [
            {
                type: 'steps',
                title: 'Add the MCP server',
                steps: ['In a chat, ask Grok Bot: "Add the MCP server at {{GATEWAY_URL}}/mcp".'],
            },
            {
                type: 'values',
                title: 'Connection values',
                settings: 'MCP server',
                values: [
                    { label: 'URL', value: '{{GATEWAY_URL}}/mcp' },
                    { label: 'Header', value: 'Authorization: Bearer {{API_KEY}}' },
                ],
                caveats: ['Grok Bot\'s docs do not document a static auth header for custom MCP servers, so it may not be able to send this key yet.'],
            },
        ],
    },
}

const grokBuild: KindEntry = {
    id: 'grok-build',
    name: 'Grok Build',
    description: 'xAI coding agent for the terminal.',
    configLocation: '`~/.grok/config.toml` (a project `.grok/config.toml` may hold `[mcp_servers]`, but not the model)',
    apply: 'Run `grok inspect` to check the loaded config.',
    exposesA2a: false,
    surfaces: {
        inference: [
            {
                type: 'code',
                title: 'Model provider',
                language: 'toml',
                code: `[model.everstack]
model = "{{MODEL}}"
base_url = "{{GATEWAY_URL}}/v1"
name = "Everstack"
api_key = "{{API_KEY}}"
api_backend = "chat_completions"

[models]
default = "everstack"`,
                caveats: [
                    'To keep the key out of the file, use `env_key = "EVERSTACK_API_KEY"` instead of `api_key` and set that variable in your shell.',
                    '`api_backend = "responses"` also works if you prefer the Responses API.',
                ],
            },
        ],
        mcp: [
            {
                type: 'code',
                title: 'MCP server',
                language: 'toml',
                code: `[mcp_servers.everstack]
url = "{{GATEWAY_URL}}/mcp"
headers = { "Authorization" = "Bearer {{API_KEY}}" }`,
                caveats: ['To keep the key out of the file, use `bearer_token_env_var = "EVERSTACK_API_KEY"` instead of `headers`.'],
            },
            {
                type: 'code',
                title: 'Or add it with the CLI',
                language: 'bash',
                code: 'grok mcp add --transport http everstack {{GATEWAY_URL}}/mcp --header "Authorization: Bearer {{API_KEY}}"',
            },
        ],
    },
}

const metaMuse: KindEntry = {
    id: 'meta-muse',
    name: 'Meta Muse',
    description: 'Meta assistant that reaches outside tools through reviewed connectors.',
    hosted: 'Meta',
    configLocation: 'Meta\'s connector platform; there is no user config',
    apply: 'A connector works once Meta approves it.',
    exposesA2a: false,
    surfaces: {
        mcp: [
            {
                type: 'steps',
                title: 'Submit a connector',
                steps: [
                    'Submit a connector on Meta\'s connector platform.',
                    'Give the endpoint and the API key below as its connection details.',
                    'Wait for Meta\'s review (risk assessment, tool review and end-to-end testing).',
                ],
            },
            {
                type: 'values',
                title: 'Connection values',
                settings: 'connector',
                values: [
                    { label: 'Endpoint', value: '{{GATEWAY_URL}}/mcp' },
                    { label: 'API key', value: '{{API_KEY}}' },
                ],
                caveats: ['Meta Muse connectors go through Meta\'s review before they work.'],
            },
        ],
    },
}

// ─── Custom ─────────────────────────────────────────────────────────

const custom: KindEntry = {
    id: 'custom',
    name: 'Custom bot',
    description: 'Any bot with an OpenAI-compatible provider setting and an MCP client.',
    configLocation: 'Wherever your bot reads its settings or environment',
    apply: 'Restart the bot after changing its environment.',
    exposesA2a: true,
    surfaces: {
        inference: [
            {
                type: 'code',
                title: 'OpenAI-compatible environment',
                language: 'bash',
                code: `export OPENAI_BASE_URL="{{GATEWAY_URL}}/v1"
export OPENAI_API_KEY="{{API_KEY}}"`,
                caveats: ['Your bot must send the model id itself; there is no standard variable for it.'],
            },
            {
                type: 'code',
                title: 'Anthropic-compatible environment (alternative)',
                language: 'bash',
                needs: 'anthropic-messages',
                code: `export ANTHROPIC_BASE_URL="{{GATEWAY_URL}}"
export ANTHROPIC_API_KEY="{{API_KEY}}"`,
                caveats: ['Leave `/v1` off the base URL; the Anthropic SDKs add it.'],
            },
        ],
        mcp: [
            {
                type: 'code',
                title: 'Claude Code (.mcp.json)',
                language: 'json',
                code: JSON.stringify(
                    { mcpServers: { everstack: { type: 'http', url: '{{GATEWAY_URL}}/mcp', headers: { Authorization: BEARER } } } },
                    null,
                    2,
                ),
                caveats: ['The client must support Streamable HTTP with static headers; SSE-only or OAuth-only clients will not connect.'],
            },
            {
                type: 'code',
                title: 'Cursor (mcp.json)',
                language: 'json',
                code: JSON.stringify(
                    { mcpServers: { everstack: { url: '{{GATEWAY_URL}}/mcp', headers: { Authorization: BEARER } } } },
                    null,
                    2,
                ),
            },
            {
                type: 'code',
                title: 'VS Code (.vscode/mcp.json)',
                language: 'json',
                code: JSON.stringify(
                    { servers: { everstack: { type: 'http', url: '{{GATEWAY_URL}}/mcp', headers: { Authorization: BEARER } } } },
                    null,
                    2,
                ),
            },
        ],
        telemetry: [
            {
                type: 'code',
                title: 'OpenTelemetry environment',
                language: 'bash',
                code: `export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
export OTEL_EXPORTER_OTLP_ENDPOINT="{{GATEWAY_URL}}"   # SDK appends /v1/traces, /v1/metrics, /v1/logs
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer%20{{API_KEY}}"
export OTEL_SERVICE_NAME="my-bot"
# exact-URL alternative (not modified by the SDK):
# export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="{{GATEWAY_URL}}/v1/traces"`,
                caveats: ['The bot must use an OpenTelemetry SDK that reads these variables.'],
            },
        ],
        a2a: [
            {
                type: 'code',
                title: 'Call an agent (legacy A2A)',
                language: 'bash',
                code: `curl -s {{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}} \\
  -H "Authorization: Bearer {{API_KEY}}" \\
  -H "Content-Type: application/json" \\
  -d '{"jsonrpc":"2.0","id":"1","method":"message/send","params":{"message":{"role":"user","messageId":"m1","parts":[{"kind":"text","text":"hello"}]}}}'`,
                caveats: ['The reply is a Task whose `artifacts[0].parts[0].text` holds the answer; replies are synchronous, with no streaming or task polling.'],
            },
            {
                type: 'code',
                title: 'Call an agent (A2A 1.0)',
                language: 'bash',
                needs: 'a2a-1.0',
                code: `curl -s {{GATEWAY_URL}}/a2a/agents/{{AGENT_ID}} \\
  -H "Authorization: Bearer {{API_KEY}}" \\
  -H "A2A-Version: 1.0" \\
  -H "Content-Type: application/json" \\
  -d '{"jsonrpc":"2.0","id":"1","method":"SendMessage","params":{"message":{"role":"ROLE_USER","messageId":"m1","parts":[{"text":"hello"}]}}}'`,
            },
        ],
    },
}

export const BOT_CATALOG: KindEntry[] = [
    openclaw,
    hermes,
    nanobot,
    zeroclaw,
    nanoclaw,
    copilotkit,
    nightly,
    grokBot,
    grokBuild,
    metaMuse,
    custom,
]

const BY_ID = new Map(BOT_CATALOG.map((entry) => [entry.id, entry]))

export function kindEntry(kind: string): KindEntry | undefined {
    return BY_ID.get(kind as BotKind)
}

export function kindName(kind: string): string {
    return kindEntry(kind)?.name ?? kind
}

/** Surfaces offered for a kind: what the bot can use, capped by the gateway. */
export function offeredSurfaces(kind: string): Surface[] {
    const entry = kindEntry(kind)
    if (!entry) return []
    const backend = BACKEND_SURFACES[entry.id]
    return SURFACES.filter((s) => entry.surfaces[s] && backend.includes(s))
}

/** Checked by default when connecting: Models, Tools and Traces where offered;
 *  Agents stays off. */
export function defaultSurfaces(kind: string): Surface[] {
    return offeredSurfaces(kind).filter((s) => s !== 'a2a')
}

// ─── Rendering ──────────────────────────────────────────────────────

export const KEY_PLACEHOLDER = '<your bot key>'
export const MODEL_PLACEHOLDER = '<model>'
export const AGENT_PLACEHOLDER = '<agent id>'

export interface RenderVars {
    gatewayUrl: string
    apiKey?: string
    model?: string
    agentId?: string
}

export type RenderedBlock =
    | { type: 'code'; title: string; language: string; code: string; caveats: string[] }
    | { type: 'values'; title: string; intro: string; values: { label: string; value: string }[]; caveats: string[] }
    | { type: 'steps'; title: string; steps: string[]; caveats: string[] }

export interface RenderedSetup {
    kind: KindEntry
    merged: (RenderedBlock & { type: 'code'; note?: string }) | null
    sections: { surface: Surface; label: string; blocks: RenderedBlock[]; notes: string[] }[]
}

export function fill(template: string, vars: RenderVars): string {
    const gateway = vars.gatewayUrl.replace(/\/+$/, '')
    const values: Record<string, string> = {
        GATEWAY_URL: gateway,
        API_KEY: vars.apiKey?.trim() || KEY_PLACEHOLDER,
        MODEL: vars.model?.trim() || MODEL_PLACEHOLDER,
        AGENT_ID: vars.agentId?.trim() || AGENT_PLACEHOLDER,
    }
    return template.replace(/\{\{(GATEWAY_URL|API_KEY|MODEL|AGENT_ID)\}\}/g, (_, name: string) => values[name])
}

/** Builds the setup view for a bot: one single-file config when the kind has
 *  one, then the blocks and notes for each enabled surface. */
export function renderSetup(kind: string, surfaces: readonly string[], vars: RenderVars): RenderedSetup | null {
    const entry = kindEntry(kind)
    if (!entry) return null
    const offered = offeredSurfaces(kind)
    const enabled = new Set(offered.filter((s) => surfaces.includes(s)))
    const mergedRaw = entry.merged?.(enabled) ?? null
    const merged = mergedRaw
        ? {
              type: 'code' as const,
              title: mergedRaw.title,
              language: mergedRaw.language,
              code: fill(mergedRaw.code, vars),
              caveats: [],
              note: mergedRaw.note,
          }
        : null

    const sections = offered
        .filter((s) => enabled.has(s))
        .map((surface) => {
            const blocks: RenderedBlock[] = []
            const notes: string[] = []
            for (const block of entry.surfaces[surface] ?? []) {
                const caveats = (block.caveats ?? []).map((c) => fill(c, vars))
                const coveredByMerged = merged && block.type === 'code' && block.merged
                if (coveredByMerged || block.type === 'note') {
                    notes.push(...caveats)
                    continue
                }
                if (block.type === 'code') {
                    blocks.push({ type: 'code', title: block.title, language: block.language, code: fill(block.code, vars), caveats })
                } else if (block.type === 'values') {
                    blocks.push({
                        type: 'values',
                        title: block.title,
                        intro: `We have not confirmed the exact setting names for this version of ${entry.name}. Use these values in its ${block.settings} settings.`,
                        values: block.values.map((v) => ({ label: v.label, value: fill(v.value, vars) })),
                        caveats,
                    })
                } else {
                    blocks.push({ type: 'steps', title: block.title, steps: block.steps.map((s) => fill(s, vars)), caveats })
                }
            }
            return { surface, label: SURFACE_INFO[surface].label, blocks, notes }
        })

    return { kind: entry, merged, sections }
}

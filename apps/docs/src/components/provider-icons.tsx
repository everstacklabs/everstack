const BASE = "/providers/lobehub";

const MONO_PROVIDERS = new Set([
  "openai",
  "anthropic",
  "groq",
  "xai",
  "openrouter",
  "ollama",
  "moonshot",
  "cursor",
  "githubcopilot",
]);

function ProviderImg({ name }: { name: string }) {
  const isMono = MONO_PROVIDERS.has(name);
  return (
    <img
      src={`${BASE}/${name}.svg`}
      alt=""
      width={16}
      height={16}
      className={`size-4${isMono ? " dark:invert" : ""}`}
    />
  );
}

export function OpenAIIcon() {
  return <ProviderImg name="openai" />;
}
export function AnthropicIcon() {
  return <ProviderImg name="anthropic" />;
}
export function ClaudeIcon() {
  return <ProviderImg name="claude" />;
}
export function GeminiIcon() {
  return <ProviderImg name="gemini" />;
}
export function AzureIcon() {
  return <ProviderImg name="azure" />;
}
export function AwsIcon() {
  return <ProviderImg name="aws" />;
}
export function BedrockIcon() {
  return <ProviderImg name="bedrock" />;
}
export function VertexAIIcon() {
  return <ProviderImg name="vertexai" />;
}
export function GroqIcon() {
  return <ProviderImg name="groq" />;
}
export function TogetherIcon() {
  return <ProviderImg name="together" />;
}
export function FireworksIcon() {
  return <ProviderImg name="fireworks" />;
}
export function DeepSeekIcon() {
  return <ProviderImg name="deepseek" />;
}
export function MistralIcon() {
  return <ProviderImg name="mistral" />;
}
export function CohereIcon() {
  return <ProviderImg name="cohere" />;
}
export function XAIIcon() {
  return <ProviderImg name="xai" />;
}
export function PerplexityIcon() {
  return <ProviderImg name="perplexity" />;
}
export function CerebrasIcon() {
  return <ProviderImg name="cerebras" />;
}
export function NvidiaIcon() {
  return <ProviderImg name="nvidia" />;
}
export function OpenRouterIcon() {
  return <ProviderImg name="openrouter" />;
}
export function OllamaIcon() {
  return <ProviderImg name="ollama" />;
}
export function HuggingFaceIcon() {
  return <ProviderImg name="huggingface" />;
}
export function QwenIcon() {
  return <ProviderImg name="qwen" />;
}
export function MinimaxIcon() {
  return <ProviderImg name="minimax" />;
}
export function MoonshotIcon() {
  return <ProviderImg name="moonshot" />;
}
export function ZhipuIcon() {
  return <ProviderImg name="zhipu" />;
}
export function CursorIcon() {
  return <ProviderImg name="cursor" />;
}
export function GitHubCopilotIcon() {
  return <ProviderImg name="githubcopilot" />;
}

import { config } from "./client.js";

const HELP = `
Everstack SDK Mini App
======================
Gateway:  ${config.baseUrl}
API key:  ${config.apiKey?.slice(0, 8)}...

Commands:
  pnpm models   List models from the gateway
  pnpm chat     One-shot chat completion
  pnpm stream   Streaming chat completion
  pnpm agents   List agents
  pnpm repl     Interactive chat REPL

Configure with .env (see .env.example).
`;

console.log(HELP);

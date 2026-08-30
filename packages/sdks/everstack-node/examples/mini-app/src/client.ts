import "dotenv/config";
import Everstack from "@everstack/node";

const apiKey = process.env.EVERSTACK_API_KEY;
const baseUrl = process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089";

if (!apiKey) {
  console.error("Missing EVERSTACK_API_KEY. Copy .env.example to .env and fill it in.");
  process.exit(1);
}

export const client = new Everstack({ apiKey, baseUrl });
export const config = { apiKey, baseUrl };

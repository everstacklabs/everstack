import { client } from "./client.js";

const model = process.env.MODEL ?? "gpt-4o-mini";
const prompt =
  process.argv.slice(2).join(" ") || "Write a haiku about TypeScript.";

console.log(`Model:  ${model}`);
console.log(`Prompt: ${prompt}\n`);
process.stdout.write("Response: ");

const start = Date.now();
let firstTokenAt = 0;
let totalChunks = 0;

const stream = await client.chat.completions.create({
  model: model as never,
  messages: [{ role: "user", content: prompt }],
  max_tokens: 200,
  stream: true,
});

for await (const chunk of stream) {
  const content = chunk.choices[0]?.delta?.content;
  if (content) {
    if (!firstTokenAt) firstTokenAt = Date.now() - start;
    totalChunks++;
    process.stdout.write(content);
  }
}

const total = Date.now() - start;
console.log("\n");
console.log(`First token: ${firstTokenAt}ms`);
console.log(`Total time:  ${total}ms`);
console.log(`Chunks:      ${totalChunks}`);

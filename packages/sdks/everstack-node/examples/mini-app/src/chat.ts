import { client } from "./client.js";

const model = process.env.MODEL ?? "gpt-4o-mini";
const prompt = process.argv.slice(2).join(" ") || "What is 2 + 2? Answer in one word.";

console.log(`Model:  ${model}`);
console.log(`Prompt: ${prompt}\n`);

const start = Date.now();
const response = await client.chat.completions.create({
  model: model as never,
  messages: [
    { role: "system", content: "You are a concise, helpful assistant." },
    { role: "user", content: prompt },
  ],
  max_tokens: 200,
});
const elapsed = Date.now() - start;

console.log(`Response: ${response.choices[0]?.message?.content}\n`);
console.log(
  `Usage: ${response.usage?.prompt_tokens} in / ${response.usage?.completion_tokens} out / ${response.usage?.total_tokens} total tokens`,
);
console.log(`Latency: ${elapsed}ms`);

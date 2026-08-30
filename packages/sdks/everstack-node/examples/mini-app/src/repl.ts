import readline from "node:readline/promises";
import { stdin, stdout } from "node:process";
import { client } from "./client.js";

const model = process.env.MODEL ?? "gpt-4o-mini";

interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

const history: ChatMessage[] = [
  { role: "system", content: "You are a helpful assistant. Keep replies concise." },
];

const rl = readline.createInterface({ input: stdin, output: stdout });

console.log(`Everstack REPL  (model: ${model})  type "exit" to quit\n`);

while (true) {
  const input = (await rl.question("you> ")).trim();
  if (!input) continue;
  if (input === "exit" || input === "quit") break;
  if (input === "reset") {
    history.length = 1;
    console.log("(history cleared)\n");
    continue;
  }

  history.push({ role: "user", content: input });

  process.stdout.write("bot> ");
  try {
    const stream = await client.chat.completions.create({
      model: model as never,
      messages: history,
      stream: true,
    });

    let reply = "";
    for await (const chunk of stream) {
      const content = chunk.choices[0]?.delta?.content;
      if (content) {
        process.stdout.write(content);
        reply += content;
      }
    }
    process.stdout.write("\n\n");
    history.push({ role: "assistant", content: reply });
  } catch (err) {
    console.error(`\n[error] ${err instanceof Error ? err.message : err}\n`);
    history.pop();
  }
}

rl.close();
console.log("bye");

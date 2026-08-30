"""Basic chat completion example."""

from everstack import Everstack

client = Everstack(api_key="pk_...")

# Simple chat completion
response = client.chat.completions.create(
    model="@openai/gpt-4o",
    messages=[{"role": "user", "content": "What is the capital of France?"}],
)
print(response.choices[0].message.content)

# Streaming
for chunk in client.chat.completions.create(
    model="@anthropic/claude-3-5-sonnet-20241022",
    messages=[{"role": "user", "content": "Tell me a short joke"}],
    stream=True,
):
    if chunk.choices and chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)
print()

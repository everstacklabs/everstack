"""Responses API example — agentic orchestration."""

from everstack import Everstack

client = Everstack(api_key="pk_...")

# Non-streaming response
response = client.responses.create(
    model="@openai/gpt-4o",
    input=[{"role": "user", "content": "Summarize the latest AI news"}],
    tools=[
        {
            "type": "function",
            "function": {
                "name": "web_search",
                "description": "Search the web",
                "parameters": {"type": "object", "properties": {"query": {"type": "string"}}},
            },
        }
    ],
)
print(f"Status: {response.status}")
for item in response.output:
    print(f"  [{item.type}] {item.content}")

# Streaming response
for event in client.responses.create(
    model="@anthropic/claude-3-5-sonnet-20241022",
    input=[{"role": "user", "content": "Hello!"}],
    stream=True,
):
    print(event)

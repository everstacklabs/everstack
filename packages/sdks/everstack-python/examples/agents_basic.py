"""Agents resource example."""

from everstack import Everstack

client = Everstack(api_key="pk_...")

# List agents
agents = client.agents.list()
for agent in agents.get("agents", []):
    print(f"  {agent['name']} ({agent['id']})")

# Create a session
session = client.agents.sessions.create(agent_id="agent_abc123")
print(f"Session: {session['id']}")

# Send a message
response = client.agents.sessions.send_message(
    session_id=session["id"],
    message="Hello, agent!",
)
print(f"Response: {response}")

# List memories
memories = client.agents.memories.list(agent_id="agent_abc123")
for mem in memories.get("memories", []):
    print(f"  Memory: {mem['content'][:80]}...")

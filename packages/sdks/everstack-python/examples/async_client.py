"""Async client example."""

import asyncio

from everstack import AsyncEverstack


async def main() -> None:
    async with AsyncEverstack(api_key="pk_...") as client:
        # Chat completion
        response = await client.chat.completions.create(
            model="@openai/gpt-4o",
            messages=[{"role": "user", "content": "Hello!"}],
        )
        print(response.choices[0].message.content)

        # Embeddings
        embeddings = await client.embeddings.create(
            model="@openai/text-embedding-3-small",
            input=["Hello world", "How are you?"],
        )
        print(f"Got {len(embeddings['data'])} embeddings")

        # List models
        models = await client.models.list()
        print(f"Available models: {len(models['data'])}")


asyncio.run(main())

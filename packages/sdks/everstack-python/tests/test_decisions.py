import json

import pytest

from everstack import AsyncEverstack, Everstack

RESPONSE = {
    "model": "@kev/kev-latest",
    "answers": {"q": {"type": "noul", "noul": 0}},
    "usage": {"input_tokens": 4, "output_tokens": 0},
}


def test_decision_protocol(httpx_mock):
    httpx_mock.add_response(url="https://example.test/v1/systemone", json=RESPONSE)
    with Everstack(api_key="pk_test", base_url="https://example.test") as client:
        result = client.decisions.create(
            model="@kev/kev-latest", state={"text": "hi"}, questions={"q": {"type": "noul"}}
        )
    assert result.answers["q"].noul == 0
    assert result.usage.input_tokens == 4
    assert json.loads(httpx_mock.get_request().content)["state"] == {"text": "hi"}


@pytest.mark.asyncio
async def test_async_decision_protocol(httpx_mock):
    httpx_mock.add_response(url="https://example.test/v1/systemone", json=RESPONSE)
    async with AsyncEverstack(api_key="pk_test", base_url="https://example.test") as client:
        result = await client.decisions.create(
            model="@kev/kev-latest", state="hi", questions={"q": {"type": "noul"}}
        )
    assert result.answers["q"].noul == 0

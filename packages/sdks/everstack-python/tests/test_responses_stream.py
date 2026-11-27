"""Streamed Responses API frames as a native (x-evs-api-key) caller gets them.

The gateway streams the CreateResponse server stream as SSE `data:` lines of
proto-JSON: a full snapshot of the response whenever its structure changes
(a new output item or content part), a delta frame carrying only what one
item grew by, and the full response last. Delta frames have no `status` and
no `output`; the text is in `result.delta.content_delta` (message text) or
`result.delta.arguments_delta` (function call arguments). Zero-valued fields,
such as `output_index` 0, are omitted.
"""

import json

import pytest

from everstack import AsyncEverstack, Everstack


def _snapshot(status, output):
    return {
        "result": {
            "id": "resp_1",
            "object": "response",
            "status": status,
            "model": "gpt-5.5",
            "output": output,
        }
    }


def _message(text):
    return {
        "id": "msg_1",
        "type": "message",
        "status": "in_progress",
        "role": 3,
        "content": [{"type": "text", "text": text}],
    }


def _call(arguments):
    item = {
        "id": "call_1",
        "type": "function_call",
        "status": "in_progress",
        "call_id": "call_1",
        "name": "get_weather",
    }
    if arguments:
        item["arguments"] = arguments
    return item


FRAMES = [
    _snapshot("in_progress", []),
    _snapshot("in_progress", [_message("Hel")]),
    {
        "result": {
            "id": "resp_1",
            "object": "response",
            "delta": {"item_id": "msg_1", "content_delta": "lo"},
        }
    },
    _snapshot("in_progress", [_message("Hello"), _call("")]),
    {
        "result": {
            "id": "resp_1",
            "object": "response",
            "delta": {"item_id": "call_1", "output_index": 1, "arguments_delta": '{"city":'},
        }
    },
    {
        "result": {
            "id": "resp_1",
            "object": "response",
            "delta": {"item_id": "call_1", "output_index": 1, "arguments_delta": '"Paris"}'},
        }
    },
    _snapshot("completed", [_message("Hello"), _call('{"city":"Paris"}')]),
]

SSE_BODY = "".join(f"data: {json.dumps(f)}\n\n" for f in FRAMES).encode()


def _accumulate(frames):
    """The README recipe: rebuild each item from snapshots plus deltas."""
    items = {}
    for frame in frames:
        res = frame.get("result", {})
        delta = res.get("delta")
        if delta:
            item = items.setdefault(delta["item_id"], {"text": "", "arguments": ""})
            item["text"] += delta.get("content_delta", "")
            item["arguments"] += delta.get("arguments_delta", "")
            continue
        for out in res.get("output", []):
            text = "".join(p.get("text", "") for p in out.get("content", []))
            items[out["id"]] = {"text": text, "arguments": out.get("arguments", "")}
    return items


class TestResponsesStream:
    def test_delta_frames_reach_the_caller(self, httpx_mock):
        httpx_mock.add_response(
            url="http://test/v1/responses",
            method="POST",
            content=SSE_BODY,
            headers={"Content-Type": "text/event-stream"},
        )
        with Everstack(api_key="pk_test", base_url="http://test") as client:
            frames = list(client.responses.create(model="gpt-5.5", input=[], stream=True))

        assert frames == FRAMES
        deltas = [f["result"]["delta"] for f in frames if "delta" in f["result"]]
        assert deltas[0] == {"item_id": "msg_1", "content_delta": "lo"}
        assert "".join(d.get("arguments_delta", "") for d in deltas) == '{"city":"Paris"}'
        assert frames[-1]["result"]["status"] == "completed"

        items = _accumulate(frames)
        assert items["msg_1"]["text"] == "Hello"
        assert items["call_1"]["arguments"] == '{"city":"Paris"}'

    @pytest.mark.asyncio
    async def test_async_delta_frames_reach_the_caller(self, httpx_mock):
        httpx_mock.add_response(
            url="http://test/v1/responses",
            method="POST",
            content=SSE_BODY,
            headers={"Content-Type": "text/event-stream"},
        )
        async with AsyncEverstack(api_key="pk_test", base_url="http://test") as client:
            stream = await client.responses.create(model="gpt-5.5", input=[], stream=True)
            frames = [f async for f in stream]

        assert frames == FRAMES
        assert _accumulate(frames)["msg_1"]["text"] == "Hello"

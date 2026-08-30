"""Tests for Pydantic response types."""

from everstack import (
    ChatCompletionResponse,
    ChatCompletionChunk,
    EmbeddingsResponse,
    ImageResponse,
    ModerationResponse,
    RerankResponse,
    ResponseObject,
    DeleteResponseResult,
    ListResponsesResult,
)


class TestChatTypes:
    def test_chat_response_from_dict(self):
        data = {
            "id": "chatcmpl-123",
            "object": "chat.completion",
            "created": 1700000000,
            "model": "@openai/gpt-4o",
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "Hello!"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
        }
        resp = ChatCompletionResponse.model_validate(data)
        assert resp.id == "chatcmpl-123"
        assert resp.choices[0].message.content == "Hello!"
        assert resp.usage.total_tokens == 15

    def test_chat_chunk_from_dict(self):
        data = {
            "id": "chatcmpl-123",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "@openai/gpt-4o",
            "choices": [
                {"index": 0, "delta": {"content": "Hi"}, "finish_reason": None}
            ],
        }
        chunk = ChatCompletionChunk.model_validate(data)
        assert chunk.choices[0].delta.content == "Hi"

    def test_chat_response_defaults(self):
        resp = ChatCompletionResponse()
        assert resp.id == ""
        assert resp.choices == []
        assert resp.usage.total_tokens == 0


class TestEmbeddingsTypes:
    def test_embeddings_response(self):
        data = {
            "object": "list",
            "data": [{"object": "embedding", "embedding": [0.1, 0.2, 0.3], "index": 0}],
            "model": "@openai/text-embedding-3-small",
            "usage": {"prompt_tokens": 5, "total_tokens": 5},
        }
        resp = EmbeddingsResponse.model_validate(data)
        assert len(resp.data) == 1
        assert resp.data[0].embedding == [0.1, 0.2, 0.3]


class TestImageTypes:
    def test_image_response(self):
        data = {
            "created": 1700000000,
            "data": [{"url": "https://example.com/img.png", "revised_prompt": "a cat"}],
            "model": "@openai/dall-e-3",
        }
        resp = ImageResponse.model_validate(data)
        assert resp.data[0].url == "https://example.com/img.png"


class TestModerationTypes:
    def test_moderation_response(self):
        data = {
            "id": "modr-123",
            "model": "text-moderation-latest",
            "results": [
                {
                    "flagged": False,
                    "categories": {"hate": False, "violence": False},
                    "category_scores": {"hate": 0.001, "violence": 0.002},
                }
            ],
        }
        resp = ModerationResponse.model_validate(data)
        assert not resp.results[0].flagged


class TestRerankTypes:
    def test_rerank_response(self):
        data = {
            "id": "rr-123",
            "model": "@cohere/rerank-v3.5",
            "results": [
                {"index": 2, "relevance_score": 0.99, "document": "best doc"},
                {"index": 0, "relevance_score": 0.45},
            ],
        }
        resp = RerankResponse.model_validate(data)
        assert resp.results[0].relevance_score == 0.99
        assert resp.results[1].document is None


class TestResponseTypes:
    def test_response_object(self):
        data = {
            "id": "resp-123",
            "object": "response",
            "created_at": 1700000000,
            "status": "completed",
            "model": "@openai/gpt-4o",
            "output": [
                {"id": "item-1", "type": "message", "status": "completed", "role": "assistant"}
            ],
            "usage": {"input_tokens": 10, "output_tokens": 20, "total_tokens": 30},
        }
        resp = ResponseObject.model_validate(data)
        assert resp.status == "completed"
        assert resp.output[0].role == "assistant"

    def test_delete_result(self):
        data = {"id": "resp-123", "object": "response", "deleted": True}
        resp = DeleteResponseResult.model_validate(data)
        assert resp.deleted

    def test_list_result(self):
        data = {"data": [], "first_id": "", "last_id": "", "has_more": False}
        resp = ListResponsesResult.model_validate(data)
        assert not resp.has_more

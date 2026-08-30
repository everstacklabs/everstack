"""Tests for the transport layer."""

import json

import httpx
import pytest

from everstack._transport import Transport, AsyncTransport, _clean_params
from everstack._errors import AuthenticationError, NotFoundError, TimeoutError


class TestTransportHeaders:
    def test_sets_api_key_header(self):
        t = Transport(
            base_url="http://localhost:8080",
            api_key="pk_test",
        )
        assert t._client.headers["x-evs-api-key"] == "pk_test"
        t.close()

    def test_sets_provider_header(self):
        t = Transport(
            base_url="http://localhost:8080",
            api_key="pk_test",
            provider="@openai",
        )
        assert t._client.headers["x-evs-provider"] == "@openai"
        t.close()

    def test_sets_org_and_user_headers(self):
        t = Transport(
            base_url="http://localhost:8080",
            api_key="pk_test",
            org_id="org_123",
            user_id="user_456",
        )
        assert t._client.headers["x-evs-org-id"] == "org_123"
        assert t._client.headers["x-evs-user-id"] == "user_456"
        t.close()

    def test_custom_headers(self):
        t = Transport(
            base_url="http://localhost:8080",
            api_key="pk_test",
            headers={"x-custom": "value"},
        )
        assert t._client.headers["x-custom"] == "value"
        t.close()

    def test_strips_trailing_slash(self):
        t = Transport(
            base_url="http://localhost:8080/",
            api_key="pk_test",
        )
        assert t._base_url == "http://localhost:8080"
        t.close()


class TestTransportRequest:
    def test_successful_get(self, httpx_mock):
        httpx_mock.add_response(json={"data": [{"id": "m1"}]})
        t = Transport(base_url="http://test", api_key="pk_test")
        result = t.request("GET", "/v1/models")
        assert result["data"][0]["id"] == "m1"
        t.close()

    def test_successful_post(self, httpx_mock):
        httpx_mock.add_response(
            json={
                "id": "chatcmpl-1",
                "choices": [{"message": {"content": "Hi"}}],
            }
        )
        t = Transport(base_url="http://test", api_key="pk_test")
        result = t.request(
            "POST",
            "/v1/chat/completions",
            json_body={"model": "@openai/gpt-4o", "messages": []},
        )
        assert result["id"] == "chatcmpl-1"
        t.close()

    def test_401_raises_auth_error(self, httpx_mock):
        httpx_mock.add_response(
            status_code=401,
            json={"message": "Invalid API key"},
        )
        t = Transport(base_url="http://test", api_key="bad_key")
        with pytest.raises(AuthenticationError):
            t.request("GET", "/v1/models")
        t.close()

    def test_404_raises_not_found(self, httpx_mock):
        httpx_mock.add_response(
            status_code=404,
            json={"message": "Not found"},
        )
        t = Transport(base_url="http://test", api_key="pk_test")
        with pytest.raises(NotFoundError):
            t.request("GET", "/v1/nonexistent")
        t.close()

    def test_204_returns_none(self, httpx_mock):
        httpx_mock.add_response(status_code=204)
        t = Transport(base_url="http://test", api_key="pk_test")
        result = t.request("DELETE", "/v1/something")
        assert result is None
        t.close()


class TestCleanParams:
    def test_none_input(self):
        assert _clean_params(None) is None

    def test_removes_none_values(self):
        assert _clean_params({"a": "1", "b": None, "c": "3"}) == {"a": "1", "c": "3"}

    def test_converts_to_strings(self):
        assert _clean_params({"limit": 10, "offset": 0}) == {"limit": "10", "offset": "0"}


@pytest.fixture
def httpx_mock(monkeypatch):
    """Minimal httpx mock fixture."""

    class MockTransport(httpx.BaseTransport):
        def __init__(self):
            self._responses = []

        def add_response(self, *, status_code=200, json=None, text=None):
            body = json if json is not None else text or ""
            self._responses.append((status_code, body))

        def handle_request(self, request):
            if not self._responses:
                raise RuntimeError("No mock response configured")
            status_code, body = self._responses.pop(0)
            if isinstance(body, dict) or isinstance(body, list):
                content = httpx.Response(
                    status_code=status_code,
                    content=__import__("json").dumps(body).encode(),
                    headers={"content-type": "application/json"},
                )
                return content
            return httpx.Response(
                status_code=status_code,
                content=body.encode() if isinstance(body, str) else body,
            )

    mock = MockTransport()

    original_init = httpx.Client.__init__

    def patched_init(self_client, *args, **kwargs):
        kwargs["transport"] = mock
        original_init(self_client, *args, **kwargs)

    monkeypatch.setattr(httpx.Client, "__init__", patched_init)
    return mock

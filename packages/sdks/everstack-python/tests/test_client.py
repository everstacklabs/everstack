"""Tests for the Everstack client."""

import pytest

from everstack import Everstack, AsyncEverstack


class TestClientInit:
    def test_creates_with_api_key(self):
        client = Everstack(api_key="pk_test")
        assert client._api_key == "pk_test"
        client.close()

    def test_default_resources_exist(self):
        client = Everstack(api_key="pk_test")
        assert client.chat is not None
        assert client.embeddings is not None
        assert client.models is not None
        assert client.audio is not None
        assert client.images is not None
        assert client.moderations is not None
        assert client.rerank is not None
        assert client.responses is not None
        assert client.agents is not None
        assert client.datasets is not None
        assert client.evaluations is not None
        assert client.observability is not None
        client.close()

    def test_context_manager(self):
        with Everstack(api_key="pk_test") as client:
            assert client._api_key == "pk_test"

    def test_custom_base_url(self):
        client = Everstack(api_key="pk_test", base_url="http://localhost:8080")
        assert client._transport._base_url == "http://localhost:8080"
        client.close()


class TestAsyncClientInit:
    def test_creates_with_api_key(self):
        client = AsyncEverstack(api_key="pk_test")
        assert client._api_key == "pk_test"

    def test_default_resources_exist(self):
        client = AsyncEverstack(api_key="pk_test")
        assert client.chat is not None
        assert client.embeddings is not None
        assert client.models is not None
        assert client.audio is not None
        assert client.images is not None
        assert client.moderations is not None
        assert client.rerank is not None
        assert client.responses is not None
        assert client.agents is not None
        assert client.datasets is not None
        assert client.evaluations is not None
        assert client.observability is not None

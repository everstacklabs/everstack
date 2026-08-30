"""Tests for error classes."""

import pytest

from everstack import (
    EverstackError,
    APIError,
    AuthenticationError,
    PermissionDeniedError,
    NotFoundError,
    RateLimitError,
    InternalServerError,
    ServiceUnavailableError,
    TimeoutError,
    ConnectionError,
    InvalidModelError,
)
from everstack._errors import _raise_for_status


class TestErrorHierarchy:
    def test_api_error_is_everstack_error(self):
        err = APIError("test", status_code=400)
        assert isinstance(err, EverstackError)

    def test_authentication_error(self):
        err = AuthenticationError()
        assert err.status_code == 401
        assert isinstance(err, APIError)

    def test_permission_denied_error(self):
        err = PermissionDeniedError()
        assert err.status_code == 403

    def test_not_found_error(self):
        err = NotFoundError()
        assert err.status_code == 404

    def test_rate_limit_error(self):
        err = RateLimitError(retry_after=30.0)
        assert err.status_code == 429
        assert err.retry_after == 30.0

    def test_internal_server_error(self):
        err = InternalServerError()
        assert err.status_code == 500

    def test_service_unavailable_error(self):
        err = ServiceUnavailableError()
        assert err.status_code == 503

    def test_timeout_error(self):
        err = TimeoutError("timed out")
        assert isinstance(err, EverstackError)

    def test_connection_error(self):
        err = ConnectionError("failed")
        assert isinstance(err, EverstackError)

    def test_invalid_model_error(self):
        err = InvalidModelError("bad-model")
        assert err.model == "bad-model"
        assert "bad-model" in str(err)

    def test_api_error_repr(self):
        err = APIError("something broke", status_code=500, code="internal")
        assert "500" in repr(err)
        assert "something broke" in repr(err)


class TestRaiseForStatus:
    def test_no_error_for_2xx(self):
        _raise_for_status(200, {})
        _raise_for_status(201, {})
        _raise_for_status(204, {})

    def test_401_raises_authentication(self):
        with pytest.raises(AuthenticationError):
            _raise_for_status(401, {"message": "bad key"})

    def test_403_raises_permission_denied(self):
        with pytest.raises(PermissionDeniedError):
            _raise_for_status(403, {"message": "nope"})

    def test_404_raises_not_found(self):
        with pytest.raises(NotFoundError):
            _raise_for_status(404, {"message": "gone"})

    def test_429_raises_rate_limit(self):
        with pytest.raises(RateLimitError):
            _raise_for_status(429, {"message": "slow down"})

    def test_500_raises_internal(self):
        with pytest.raises(InternalServerError):
            _raise_for_status(500, {"message": "oops"})

    def test_503_raises_service_unavailable(self):
        with pytest.raises(ServiceUnavailableError):
            _raise_for_status(503, {"message": "down"})

    def test_unknown_status_raises_api_error(self):
        with pytest.raises(APIError) as exc_info:
            _raise_for_status(418, {"message": "teapot"})
        assert exc_info.value.status_code == 418

    def test_nested_error_body(self):
        with pytest.raises(APIError) as exc_info:
            _raise_for_status(400, {"error": {"message": "nested msg", "code": "bad_request"}})
        assert exc_info.value.message == "nested msg"

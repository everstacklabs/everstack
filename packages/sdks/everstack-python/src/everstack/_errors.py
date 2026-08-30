"""Everstack SDK error hierarchy."""

from __future__ import annotations

from typing import Dict, Optional, Type


class EverstackError(Exception):
    """Base error for all Everstack SDK errors."""

    def __init__(self, message: str) -> None:
        super().__init__(message)
        self.message = message


class APIError(EverstackError):
    """An error returned by the Everstack API."""

    status_code: int
    code: Optional[str]
    param: Optional[str]

    def __init__(
        self,
        message: str,
        *,
        status_code: int,
        code: Optional[str] = None,
        param: Optional[str] = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.code = code
        self.param = param

    def __repr__(self) -> str:
        return f"{self.__class__.__name__}(status_code={self.status_code}, message={self.message!r})"


class AuthenticationError(APIError):
    """401 — Invalid or missing API key."""

    def __init__(self, message: str = "Invalid API key") -> None:
        super().__init__(message, status_code=401, code="authentication_error")


class PermissionDeniedError(APIError):
    """403 — Insufficient permissions."""

    def __init__(self, message: str = "Permission denied") -> None:
        super().__init__(message, status_code=403, code="permission_denied")


class NotFoundError(APIError):
    """404 — Resource not found."""

    def __init__(self, message: str = "Not found") -> None:
        super().__init__(message, status_code=404, code="not_found")


class RateLimitError(APIError):
    """429 — Rate limit exceeded."""

    retry_after: Optional[float]

    def __init__(
        self,
        message: str = "Rate limit exceeded",
        *,
        retry_after: Optional[float] = None,
    ) -> None:
        super().__init__(message, status_code=429, code="rate_limit_exceeded")
        self.retry_after = retry_after


class InternalServerError(APIError):
    """500 — Internal server error."""

    def __init__(self, message: str = "Internal server error") -> None:
        super().__init__(message, status_code=500, code="internal_error")


class ServiceUnavailableError(APIError):
    """503 — Service temporarily unavailable."""

    def __init__(self, message: str = "Service unavailable") -> None:
        super().__init__(message, status_code=503, code="service_unavailable")


class TimeoutError(EverstackError):
    """Request timed out."""

    pass


class ConnectionError(EverstackError):
    """Could not connect to the API."""

    pass


class InvalidModelError(EverstackError):
    """Invalid model identifier."""

    def __init__(self, model: str) -> None:
        super().__init__(f"Invalid model: {model}")
        self.model = model


_STATUS_MAP: Dict[int, Type[APIError]] = {
    401: AuthenticationError,
    403: PermissionDeniedError,
    404: NotFoundError,
    429: RateLimitError,
    500: InternalServerError,
    503: ServiceUnavailableError,
}


def _raise_for_status(status_code: int, body: Dict[str, object]) -> None:
    """Raise the appropriate error for an API error response."""
    if status_code < 400:
        return

    message = body.get("message") or body.get("error", {}).get("message", "Unknown error")
    code = body.get("code") or body.get("error", {}).get("code")

    error_cls = _STATUS_MAP.get(status_code, APIError)

    if error_cls is RateLimitError:
        raise RateLimitError(message)

    if error_cls is APIError:
        raise APIError(message, status_code=status_code, code=code)

    raise error_cls(message)

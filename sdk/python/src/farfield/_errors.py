from __future__ import annotations


class FarfieldError(Exception):
    """Base class for actionable Farfield SDK errors."""


class APIError(FarfieldError):
    def __init__(self, status_code: int, code: str, message: str, *, retryable: bool) -> None:
        super().__init__(f"{code}: {message}" if code else f"HTTP {status_code}: {message}")
        self.status_code = status_code
        self.code = code
        self.message = message
        self.retryable = retryable


class TransportError(FarfieldError):
    pass


class DroppedEvent(FarfieldError):
    """Raised when before_send intentionally drops an event."""

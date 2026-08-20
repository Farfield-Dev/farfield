from __future__ import annotations

from contextvars import ContextVar, Token

from ._models import Scope

_active_scope: ContextVar[Scope | None] = ContextVar("farfield_scope", default=None)


def get_scope() -> Scope | None:
    return _active_scope.get()


def set_scope(scope: Scope) -> Token[Scope | None]:
    return _active_scope.set(scope)


def reset_scope(token: Token[Scope | None]) -> None:
    _active_scope.reset(token)

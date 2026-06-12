from typing import Annotated

from fastapi import Depends

from app.auth.service import AuthService


# Function-style dependency that wraps a class-based dependency —
# standard FastAPI pattern for "require this to be valid before the
# handler runs". The returned AuthService instance is what the
# handler ends up with when it declares `auth: AuthService = Depends(require_auth)`.
def require_auth(auth: AuthService = Depends(AuthService)) -> AuthService:
    return auth


# PEP 593 / modern FastAPI shorthand — same resolution as the default-
# value form but typed via Annotated. Both shapes must resolve to
# AuthService.validate for any call-chain query from a handler using
# `auth: CurrentAuth` to reach the underlying implementation.
CurrentAuth = Annotated[AuthService, Depends(AuthService)]

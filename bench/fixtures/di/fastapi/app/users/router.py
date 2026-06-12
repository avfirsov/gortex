from typing import Annotated

from fastapi import APIRouter, Depends

from app.auth.deps import CurrentAuth, require_auth
from app.auth.service import AuthService
from app.config.settings import Settings, get_settings
from app.users.models import User
from app.users.service import UserService

router = APIRouter(prefix="/users")


# Default-value form: `users: UserService = Depends(UserService)`.
# Most common shape in real FastAPI code.
@router.get("/")
def list_users(users: UserService = Depends(UserService)) -> list[User]:
    return users.find_all()


# Nested / chained Depends: the handler depends on require_auth, which
# itself depends on AuthService, which depends on UserService. Whole
# chain is discoverable statically — each Depends() call exposes its
# target function/class as a default-value expression.
@router.get("/{user_id}")
def get_user(
    user_id: str,
    users: UserService = Depends(UserService),
    auth: AuthService = Depends(require_auth),
) -> User:
    u = users.find_one(user_id)
    if u is None:
        raise ValueError(f"no user {user_id}")
    # Touch the auth-resolved service so the call chain from get_user
    # reaches AuthService.validate and by extension UserService.find_one.
    _ = auth.validate(user_id, f"tok_{u.id}")
    return u


# Annotated-form Depends (PEP 593): `auth: Annotated[AuthService, Depends(...)]`.
# Semantically equivalent to the default-value form; a modern FastAPI
# codebase uses it everywhere.
@router.post("/")
def create_user(
    email: str,
    name: str,
    users: Annotated[UserService, Depends(UserService)],
    settings: Annotated[Settings, Depends(get_settings)],
) -> User:
    _ = settings.database_url
    return users.create(email, name)

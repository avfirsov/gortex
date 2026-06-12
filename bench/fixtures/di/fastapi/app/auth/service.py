from fastapi import Depends, HTTPException

from app.users.service import UserService


class AuthService:
    """Class-based dependency that ITSELF depends on another service.
    FastAPI resolves the nested Depends chain automatically; statically
    the dependency is visible via the __init__ parameter's Depends()
    default value."""

    def __init__(self, users: UserService = Depends(UserService)) -> None:
        self._users = users

    def validate(self, user_id: str, token: str) -> bool:
        u = self._users.find_one(user_id)
        if u is None or token != f"tok_{u.id}":
            raise HTTPException(status_code=401)
        return True

from typing import Optional

from app.users.models import User


class UserService:
    """Plain service class — registered as a FastAPI dependency via
    class-based `Depends(UserService)`. Its methods are called from
    route handlers that receive the instance through a parameter."""

    def __init__(self) -> None:
        self._users: dict[str, User] = {}

    def find_one(self, user_id: str) -> Optional[User]:
        return self._users.get(user_id)

    def find_all(self) -> list[User]:
        return list(self._users.values())

    def create(self, email: str, name: str) -> User:
        uid = f"usr_{len(self._users) + 1}"
        u = User(id=uid, email=email, name=name)
        self._users[uid] = u
        return u

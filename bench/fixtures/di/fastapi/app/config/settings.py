from dataclasses import dataclass


@dataclass
class Settings:
    database_url: str = "postgres://localhost/test"
    feature_flags: dict[str, bool] = None


def get_settings() -> Settings:
    """Factory-style dependency. FastAPI will call this once per request
    (unless cached with @lru_cache) and inject the result into any
    handler that declares `settings: Settings = Depends(get_settings)`."""
    return Settings(feature_flags={"beta": True})

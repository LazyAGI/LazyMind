from typing import Callable


def permission_required(*permissions: str):
    """Static marker consumed by backend/scripts/extract_api_permissions.py."""

    def decorator(function: Callable):
        function.__required_permissions__ = set(permissions)
        return function

    return decorator

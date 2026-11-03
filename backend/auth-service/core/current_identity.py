"""Current account identity shared by public JWT and trusted runtime callers."""
from core.database import SessionLocal
from core.errors import ErrorCodes, raise_error
from core.permissions import get_effective_permission_codes
from repositories import UserRepository


def current_identity(user_id):
    with SessionLocal() as db:
        user = UserRepository.get_by_id(
            db, user_id, load_role=True, load_permission_groups=True,
            load_groups=True, load_group_permission_groups=True,
        )
    if not user or user.disabled:
        raise_error(ErrorCodes.UNAUTHORIZED)
    return {
        'sub': str(user.id), 'role': user.role.name,
        'tenant_id': user.tenant_id,
        'permissions': sorted(get_effective_permission_codes(user)),
    }

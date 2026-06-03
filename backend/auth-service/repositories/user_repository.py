import uuid

from sqlalchemy.orm import Session, joinedload
from models import Group, Role, User, UserGroup


class UserRepository:

    def __init__(self):
        self.model = User

    def _get_by_id(
        self,
        session: Session,
        user_id: uuid.UUID,
        *,
        load_role: bool = False,
        load_permission_groups: bool = False,
        load_groups: bool = False,
        load_group_permission_groups: bool = False,
    ) -> User | None:
        q = session.query(self.model).filter_by(id=user_id)
        if load_role:
            q = q.options(joinedload(User.role))
            if load_permission_groups:
                q = q.options(joinedload(User.role).joinedload(Role.permission_groups))
        if load_groups:
            q = q.options(joinedload(User.groups).joinedload(UserGroup.group))
            if load_group_permission_groups:
                q = q.options(
                    joinedload(User.groups).joinedload(UserGroup.group).joinedload(Group.permission_groups)
                )
        return q.first()

    def _get_by_username(self, session: Session, username: str) -> User | None:
        return session.query(self.model).filter_by(username=username).first()

    def _count(self, session: Session) -> int:
        return session.query(self.model).count()

    def _create(
        self,
        session: Session,
        username: str,
        password_hash: str,
        role_id: uuid.UUID,
        tenant_id: str = '',
        email: str | None = None,
        display_name: str = '',
        phone: str = '',
        remark: str = '',
        creator: str = '',
        disabled: bool = False,
        source: str = 'platform',
    ) -> User:
        user = self.model(
            username=username,
            password_hash=password_hash,
            role_id=role_id,
            tenant_id=tenant_id,
            email=email,
            display_name=display_name or username,
            phone=phone,
            remark=remark,
            creator=creator,
            disabled=disabled,
            source=source,
        )
        session.add(user)
        session.commit()
        session.refresh(user)
        return user

    def _list_paginated(
        self,
        session: Session,
        page: int = 1,
        page_size: int = 20,
        search: str | None = None,
        tenant_id: str | None = None,
        active_only: bool = False,
    ) -> tuple[list[User], int]:
        q = session.query(self.model).options(joinedload(User.role)).order_by(self.model.id)
        count_q = session.query(self.model)
        if search:
            like = f'%{search}%'
            q = q.filter(
                self.model.username.ilike(like) | self.model.display_name.ilike(like)
            )
            count_q = count_q.filter(
                self.model.username.ilike(like) | self.model.display_name.ilike(like)
            )
        if tenant_id is not None:
            q = q.filter(self.model.tenant_id == tenant_id)
            count_q = count_q.filter(self.model.tenant_id == tenant_id)
        if active_only:
            q = q.filter(self.model.disabled.is_(False))
            count_q = count_q.filter(self.model.disabled.is_(False))
        total = count_q.count()
        users = q.offset((page - 1) * page_size).limit(page_size).all()
        return users, total

    @classmethod
    def get_by_id(
        cls,
        session: Session,
        user_id: uuid.UUID,
        *,
        load_role: bool = False,
        load_permission_groups: bool = False,
        load_groups: bool = False,
        load_group_permission_groups: bool = False,
    ) -> User | None:
        return cls()._get_by_id(
            session,
            user_id,
            load_role=load_role,
            load_permission_groups=load_permission_groups,
            load_groups=load_groups,
            load_group_permission_groups=load_group_permission_groups,
        )

    @classmethod
    def get_by_username(cls, session: Session, username: str) -> User | None:
        return cls()._get_by_username(session, username)

    @classmethod
    def count(cls, session: Session) -> int:
        return cls()._count(session)

    @classmethod
    def create(
        cls,
        session: Session,
        username: str,
        password_hash: str,
        role_id: uuid.UUID,
        tenant_id: str = '',
        email: str | None = None,
        display_name: str = '',
        phone: str = '',
        remark: str = '',
        creator: str = '',
        disabled: bool = False,
        source: str = 'platform',
    ) -> User:
        return cls()._create(
            session, username, password_hash, role_id, tenant_id, email,
            display_name, phone, remark, creator, disabled, source,
        )

    @classmethod
    def list_paginated(
        cls,
        session: Session,
        page: int = 1,
        page_size: int = 20,
        search: str | None = None,
        tenant_id: str | None = None,
        active_only: bool = False,
    ) -> tuple[list[User], int]:
        return cls()._list_paginated(session, page, page_size, search, tenant_id, active_only)

    @classmethod
    def update_profile(
        cls,
        session: Session,
        user_id: uuid.UUID,
        *,
        display_name: str | None = None,
        email: str | None = None,
        phone: str | None = None,
        remark: str | None = None,
    ) -> User | None:
        """Update user profile fields (only non-None values are applied)."""
        user = cls.get_by_id(session, user_id)
        if not user:
            return None
        if display_name is not None:
            user.display_name = display_name
        if email is not None:
            user.email = email
        if phone is not None:
            user.phone = phone
        if remark is not None:
            user.remark = remark
        session.commit()
        session.refresh(user)
        return user

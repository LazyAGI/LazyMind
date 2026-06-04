from __future__ import annotations

from sqlalchemy import (
    TIMESTAMP,
    Boolean,
    Column,
    ForeignKey,
    Index,
    Integer,
    String,
    UniqueConstraint,
    func,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.orm import DeclarativeBase


class Base(DeclarativeBase):
    pass


class RouterAlgorithm(Base):
    __tablename__ = 'router_algorithms'

    id = Column(String(64), primary_key=True)
    name = Column(String(255), nullable=False)
    code_path = Column(String(512), nullable=False)
    config = Column(JSONB, nullable=False, server_default=text("'{}'"))
    # pending / active / disabled
    status = Column(String(32), nullable=False, server_default=text("'pending'"))
    created_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now())
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now(), onupdate=func.now())


class RouterAbStrategy(Base):
    __tablename__ = 'router_ab_strategies'

    id = Column(Integer, primary_key=True, autoincrement=True)
    weights = Column(JSONB, nullable=False)
    is_active = Column(Boolean, nullable=False, server_default=text('TRUE'))
    created_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now())
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now(), onupdate=func.now())


class RouterSessionAssignment(Base):
    __tablename__ = 'router_session_assignments'

    session_id = Column(String(255), primary_key=True)
    algorithm_id = Column(String(64), ForeignKey('router_algorithms.id'), nullable=False)
    assigned_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now())
    last_seen_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now())

    __table_args__ = (Index('ix_router_session_assignments_algorithm_id', 'algorithm_id'),)


class RouterInstance(Base):
    __tablename__ = 'router_instances'

    instance_id = Column(String(64), primary_key=True)
    host = Column(String(255), nullable=False)
    pid = Column(Integer, nullable=False)
    port_range_start = Column(Integer, nullable=False)
    port_range_end = Column(Integer, nullable=False)
    last_heartbeat = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now())


class RouterChildProcess(Base):
    __tablename__ = 'router_child_processes'

    id = Column(Integer, primary_key=True, autoincrement=True)
    instance_id = Column(String(64), ForeignKey('router_instances.instance_id'), nullable=False)
    algorithm_id = Column(String(64), ForeignKey('router_algorithms.id'), nullable=False)
    host = Column(String(255), nullable=False)
    port = Column(Integer, nullable=False)
    pid = Column(Integer, nullable=True)
    # starting / healthy / unhealthy / stopped
    status = Column(String(32), nullable=False, server_default=text("'starting'"))
    failures = Column(Integer, nullable=False, server_default=text('0'))
    last_health_at = Column(TIMESTAMP(timezone=True), nullable=True)
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, server_default=func.now(), onupdate=func.now())

    __table_args__ = (
        UniqueConstraint('host', 'port', name='uq_router_child_processes_host_port'),
        Index('ix_router_child_processes_algorithm_status', 'algorithm_id', 'status'),
        Index('ix_router_child_processes_instance_id', 'instance_id'),
    )

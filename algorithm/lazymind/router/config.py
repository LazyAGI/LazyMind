from __future__ import annotations

import os

from lazymind.config import config

# ---------------------------------------------------------------------------
# Router toggle
# ---------------------------------------------------------------------------
config.add(
    'enable_router',
    bool,
    False,
    'ENABLE_ROUTER',
    description='Enable router mode. When false, app.py falls back to the original chat service.',
)

# ---------------------------------------------------------------------------
# Port pool
# ---------------------------------------------------------------------------
config.add(
    'router_port_pool_start',
    int,
    18000,
    'ROUTER_PORT_POOL_START',
    description='Start of port range for child process instances.',
)
config.add(
    'router_port_pool_end',
    int,
    18999,
    'ROUTER_PORT_POOL_END',
    description='End (inclusive) of port range for child process instances.',
)
config.add(
    'router_ports_per_instance',
    int,
    100,
    'ROUTER_PORTS_PER_INSTANCE',
    description='Number of ports each router instance claims from the pool.',
)

# ---------------------------------------------------------------------------
# Default algorithm
# ---------------------------------------------------------------------------
config.add(
    'router_default_algo_path',
    str,
    '/opt/lazymind/chat',
    'ROUTER_DEFAULT_ALGO_PATH',
    description='Code path for the default algorithm (registered as id=default on startup).',
)
config.add(
    'router_default_instance_count',
    int,
    1,
    'ROUTER_DEFAULT_INSTANCE_COUNT',
    description='Number of child process instances to start for the default algorithm.',
)

# ---------------------------------------------------------------------------
# Health checker
# ---------------------------------------------------------------------------
config.add(
    'router_health_interval',
    int,
    10,
    'ROUTER_HEALTH_INTERVAL',
    description='Interval in seconds between health checks of child processes.',
)
config.add(
    'router_health_max_failures',
    int,
    3,
    'ROUTER_HEALTH_MAX_FAILURES',
    description='Number of consecutive failures before marking a child process unhealthy.',
)
config.add(
    'router_heartbeat_interval',
    int,
    10,
    'ROUTER_HEARTBEAT_INTERVAL',
    description='Interval in seconds between router instance heartbeat updates.',
)
config.add(
    'router_instance_timeout',
    int,
    30,
    'ROUTER_INSTANCE_TIMEOUT',
    description='Seconds after which a router instance with no heartbeat is considered dead.',
)
config.add(
    'router_registry_refresh_interval',
    int,
    5,
    'ROUTER_REGISTRY_REFRESH_INTERVAL',
    description='Interval in seconds for GlobalRegistry to refresh global instance view from DB.',
)
config.add(
    'router_startup_timeout',
    int,
    30,
    'ROUTER_STARTUP_TIMEOUT',
    description='Timeout in seconds to wait for a child process to become healthy on startup.',
)

# ---------------------------------------------------------------------------
# DB
# ---------------------------------------------------------------------------
config.add(
    'core_database_url',
    str,
    None,
    'CORE_DATABASE_URL',
    description='Core service PostgreSQL URL (required for router mode).',
)

# ---------------------------------------------------------------------------
# Host resolution
# ---------------------------------------------------------------------------
config.add(
    'router_host',
    str,
    None,
    'ROUTER_HOST',
    description='Hostname / IP advertised for this router instance (auto-detected if not set).',
)

# ---------------------------------------------------------------------------
# Exported constants
# ---------------------------------------------------------------------------
ENABLE_ROUTER: bool = config['enable_router']
PORT_POOL_START: int = config['router_port_pool_start']
PORT_POOL_END: int = config['router_port_pool_end']
PORTS_PER_INSTANCE: int = config['router_ports_per_instance']
DEFAULT_ALGO_PATH: str = config['router_default_algo_path']
DEFAULT_INSTANCE_COUNT: int = config['router_default_instance_count']
HEALTH_INTERVAL: int = config['router_health_interval']
HEALTH_MAX_FAILURES: int = config['router_health_max_failures']
HEARTBEAT_INTERVAL: int = config['router_heartbeat_interval']
INSTANCE_TIMEOUT: int = config['router_instance_timeout']
REGISTRY_REFRESH_INTERVAL: int = config['router_registry_refresh_interval']
STARTUP_TIMEOUT: int = config['router_startup_timeout']
CORE_DATABASE_URL: str = config['core_database_url'] or ''

# Resolve advertised host: prefer explicit env, then POD_IP (K8s), then socket hostname
_explicit_host: str | None = config['router_host']


def _resolve_host() -> str:
    if _explicit_host:
        return _explicit_host
    pod_ip = os.environ.get('POD_IP') or os.environ.get('MY_POD_IP')
    if pod_ip:
        return pod_ip
    import socket
    return socket.gethostname()


ROUTER_HOST: str = _resolve_host()

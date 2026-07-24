"""
Bootstrap conftest for tests/algorithm/lazymind/.

Installs a minimal lazymind package stub BEFORE any router submodule is
imported, preventing the real lazymind.__init__ (which depends on lazyllm)
from being triggered in this test environment.
"""
from __future__ import annotations

import os
import sys
import types

# ── Path setup ────────────────────────────────────────────────────────────────
_tests_dir = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
_root = os.path.dirname(_tests_dir)
_algo = os.path.join(_root, 'algorithm')
if _algo not in sys.path:
    sys.path.insert(0, _algo)

# ── Install lazymind + lazymind.config stubs ──────────────────────────────────
# The real lazymind/__init__.py imports lazyllm which is unavailable here.
# We replace lazymind with a minimal namespace package so its sub-packages
# (router, router.db, …) can be imported cleanly.

_ROUTER_CONFIG_DEFAULTS: dict = {
    'router_registry_refresh_interval': 5,
    'router_health_interval': 10,
    'router_health_max_failures': 3,
    'router_heartbeat_interval': 10,
    'router_instance_timeout': 30,
    'router_startup_timeout': 30,
    'router_port_pool_start': 18000,
    'router_port_pool_end': 18999,
    'router_ports_per_instance': 100,
    'router_default_algo_path': '/opt/lazymind/chat',
    'router_default_instance_count': 1,
    'router_child_processes_enabled': True,
    'router_host': '127.0.0.1',
    'core_database_url': 'sqlite+aiosqlite://',
    'enable_router': False,
    'background_jobs_enabled': True,
}


class _FakeConfig:
    def __getitem__(self, key):
        return _ROUTER_CONFIG_DEFAULTS.get(key)

    def add(self, *args, **kwargs):
        pass


def _install_stubs() -> None:
    # Ensure lazymind is a real package with __path__ (not a failed import stub)
    existing = sys.modules.get('lazymind')
    if existing is None or not hasattr(existing, '__path__'):
        pkg = types.ModuleType('lazymind')
        pkg.__path__ = [os.path.join(_algo, 'lazymind')]
        pkg.__package__ = 'lazymind'
        sys.modules['lazymind'] = pkg

    # Install the config stub (replaces broken or missing module)
    existing_cfg = sys.modules.get('lazymind.config')
    if existing_cfg is None or not hasattr(existing_cfg, 'config'):
        config_mod = types.ModuleType('lazymind.config')
        config_mod.config = _FakeConfig()  # type: ignore[attr-defined]
        sys.modules['lazymind.config'] = config_mod


_install_stubs()

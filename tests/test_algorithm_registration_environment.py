import os
from pathlib import Path
import subprocess
import sys

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[1]


@pytest.mark.parametrize('override,expected', [([], 'none'), (['LAZYLLM_ALGO_REGISTER_POLICY=update'], 'update')])
def test_dotenv_overrides_local_config_but_not_make_cli(tmp_path, override, expected):
    (tmp_path / '.env').write_text('LAZYLLM_ALGO_REGISTER_POLICY=none\n')
    (tmp_path / 'local').mkdir()
    (tmp_path / 'local/config.env').write_text('LAZYLLM_ALGO_REGISTER_POLICY=force\n')
    # Evaluate the real include/export order without invoking a runtime lifecycle target.
    makefile = (ROOT / 'Makefile').read_text() + '\nprint-policy:\n\t@echo POLICY=$(LAZYLLM_ALGO_REGISTER_POLICY)\n'
    (tmp_path / 'Makefile').write_text(makefile)
    env = dict(os.environ)
    env.pop('LAZYLLM_ALGO_REGISTER_POLICY', None)
    result = subprocess.run(['make', '--no-print-directory', 'print-policy', 'MAKECMDGOALS=local-down', *override],
                            cwd=tmp_path, env=env, text=True, capture_output=True, timeout=30)
    assert result.returncode == 0, result.stderr
    assert f'POLICY={expected}' in result.stdout


@pytest.mark.parametrize('local_profile,dotenv_profile,override,expected', [
    ('intl', 'cn', [], 'cn'),
    ('cn', 'intl', [], 'intl'),
    ('intl', 'cn', ['MIRROR_PROFILE=intl'], 'intl'),
])
def test_mirror_profile_matches_loaded_settings(tmp_path, local_profile, dotenv_profile, override, expected):
    (tmp_path / '.env').write_text(f'MIRROR_PROFILE={dotenv_profile}\n')
    (tmp_path / 'local').mkdir()
    (tmp_path / 'local/config.env').write_text(f'MIRROR_PROFILE={local_profile}\n')
    for profile in ('cn', 'intl'):
        (tmp_path / f'.env.mirrors.{profile}').write_text(f'PIP_INDEX_URL=https://{profile}.example/simple\n')
    makefile = (ROOT / 'Makefile').read_text() + (
        '\nprint-mirror:\n\t@echo PROFILE=$(MIRROR_PROFILE) PIP=$(PIP_INDEX_URL)\n'
    )
    (tmp_path / 'Makefile').write_text(makefile)
    env = dict(os.environ)
    for key in ('MIRROR_PROFILE', 'PIP_INDEX_URL', 'MAKEFLAGS', 'MAKEOVERRIDES'):
        env.pop(key, None)
    result = subprocess.run(['make', '--no-print-directory', 'print-mirror', 'MAKECMDGOALS=local-down', *override],
                            cwd=tmp_path, env=env, text=True, capture_output=True, timeout=30)
    assert result.returncode == 0, result.stderr
    assert f'PROFILE={expected} PIP=https://{expected}.example/simple' in result.stdout


def test_compose_passes_force_override_to_algorithm_service():
    compose = yaml.safe_load((ROOT / 'docker-compose.yml').read_text())
    environments = [service.get('environment', {}) for service in compose['services'].values()]
    algorithm = next(env for env in environments if 'LAZYMIND_ALGO_SERVER_PORT' in env)
    assert algorithm['LAZYMIND_FORCE_ALGO_REGISTER'] == '${LAZYMIND_FORCE_ALGO_REGISTER:-false}'
    assert algorithm['LAZYLLM_ALGO_REGISTER_POLICY'] == '${LAZYLLM_ALGO_REGISTER_POLICY:-update}'


def test_standalone_docserver_entrypoint_cold_import(tmp_path):
    env = dict(os.environ, PYTHONPATH=os.pathsep.join([str(ROOT / 'algorithm'), str(ROOT / 'algorithm/lazyllm')]),
               LAZYLLM_HOME=str(tmp_path), PYTHONDONTWRITEBYTECODE='1')
    result = subprocess.run([sys.executable, '-B', str(ROOT / 'backend/core/doc/doc_server.py'), '--help'],
                            env=env, cwd=tmp_path, capture_output=True, text=True, timeout=60)
    assert result.returncode == 0, result.stdout + result.stderr
    assert 'standalone DocServer' in result.stdout


@pytest.mark.parametrize('enabled,expected', [('true', 'force'), ('false', 'none')])
def test_force_environment_wins_over_legacy_policy_in_fresh_process(tmp_path, enabled, expected):
    env = dict(os.environ, PYTHONPATH=os.pathsep.join([str(ROOT / 'algorithm'), str(ROOT / 'algorithm/lazyllm')]),
               LAZYLLM_HOME=str(tmp_path), LAZYMIND_FORCE_ALGO_REGISTER=enabled,
               LAZYLLM_ALGO_REGISTER_POLICY='none', PYTHONDONTWRITEBYTECODE='1')
    script = ('from lazymind.parsing.service.build_document import configure_algorithm_registration; '
              f'assert configure_algorithm_registration() == {expected!r}')
    result = subprocess.run([sys.executable, '-B', '-c', script], env=env, cwd=tmp_path,
                            capture_output=True, text=True, timeout=60)
    assert result.returncode == 0, result.stdout + result.stderr

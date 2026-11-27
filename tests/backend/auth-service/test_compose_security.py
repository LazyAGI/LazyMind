from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]


def test_compose_requires_jwt_secret_and_does_not_embed_public_default():
    compose = (REPO_ROOT / 'docker-compose.yml').read_text(encoding='utf-8')

    assert 'LAZYMIND_JWT_SECRET: ${LAZYMIND_JWT_SECRET:?required}' in compose
    assert 'LAZYMIND_JWT_SECRET: dev-secret-change-me' not in compose


def test_compose_requires_bootstrap_admin_password():
    compose = (REPO_ROOT / 'docker-compose.yml').read_text(encoding='utf-8')

    required = 'LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD: ${LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD:?required}'
    assert compose.count(required) == 2
    assert 'LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD: ${LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD:-admin}' not in compose


def test_make_up_does_not_supply_public_auth_defaults():
    makefile = (REPO_ROOT / 'Makefile').read_text(encoding='utf-8')

    assert 'export LAZYMIND_JWT_SECRET ?=' not in makefile
    assert 'export LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD ?=' not in makefile

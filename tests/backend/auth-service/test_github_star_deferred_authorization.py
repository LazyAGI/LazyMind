"""Revocation committed during preflight must stop subsequent GitHub dispatch."""
import pytest
from models import CloudAuthConnection, User
from test_github_star_deferred import OTHER, OWNER, current, intent, require_service
from test_github_star_deferred import deferred_env as _shared_deferred_env  # noqa: F401


@pytest.fixture
def deferred_env(request):
    return request.getfixturevalue('_shared_deferred_env')


@pytest.mark.parametrize('source,change,after_response', [
    ('connection', 'connection_deleted', 1),
    ('connection', 'owner_disabled', 1),
    ('connection', 'owner_permission_removed', 1),
    ('connection', 'connection_owner_changed', 1),
    ('connection', 'connection_account_changed', 1),
    ('connection', 'connection_status_revoked', 1),
    ('connection', 'connection_provider_changed', 1),
    ('connection', 'connection_mode_changed', 1),
    ('connection', 'connection_deleted', 3),
    ('connection', 'owner_disabled', 3),
    ('connection', 'owner_permission_removed', 3),
    ('local', 'owner_disabled', 3),
    ('local', 'owner_permission_removed', 3),
])
def test_current_authorization_is_rechecked_after_preflight_responses(
    deferred_env, source, change, after_response,
):
    env = deferred_env
    service = require_service(env)
    body = {'source': source, 'confirmed': True}
    if source == 'connection':
        env.seed()
        body.update(connection_id='conn-deferred', expected_account_id='10001')
    accepted = intent(service.receive(owner_user_id=str(OWNER), **body))

    def revoke(count):
        if count != after_response:
            return
        if change == 'owner_permission_removed':
            env.set_permissions(['model.read'])
            return
        with env.sessions() as db:
            if change == 'owner_disabled':
                db.get(User, OWNER).disabled = True
            else:
                connection = db.get(CloudAuthConnection, 'conn-deferred')
                if change == 'connection_deleted':
                    db.delete(connection)
                elif change == 'connection_owner_changed':
                    connection.owner_user_id = str(OTHER)
                elif change == 'connection_account_changed':
                    connection.provider_account_id = '20002'
                elif change == 'connection_status_revoked':
                    connection.status = 'REVOKED'
                elif change == 'connection_provider_changed':
                    connection.provider = 'notion'
                else:
                    assert change == 'connection_mode_changed'
                    connection.auth_mode = 'tenant'
            db.commit()

    env.github.after_response = revoke
    service.run_due_once()
    stopped = current(env)
    assert stopped['status'] == 'needs_attention'
    assert stopped['remote_state'] == 'unknown'
    assert stopped['intent_id'] == accepted['intent_id']
    assert env.row()['intent_id'] == accepted['intent_id']
    assert len(env.github.calls) == after_response
    assert env.github.writes() == []
    assert env.probe.call_count == (2 if source == 'local' else 0)
    env.clock.advance(hours=6)
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert len(env.github.calls) == after_response
    assert current(env)['expires_at'] == accepted['expires_at']
    assert current(env)['attempt_count'] == 1

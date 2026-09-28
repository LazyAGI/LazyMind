"""Real gateway composition; only platform network calls are replaced in tests."""
import hashlib
import copy
import os
from pathlib import Path
import sys
from types import SimpleNamespace
import uuid

import psycopg
import httpx
from psycopg import sql
from psycopg.conninfo import make_conninfo
import pytest
from fastapi.testclient import TestClient

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / 'backend/channel-gateway'))

from channel_gateway.app import app  # noqa: E402
from channel_gateway.bootstrap import Settings, build_components  # noqa: E402
from channel_gateway.common.domain.channel import InboundEnvelope, OutboundMessage  # noqa: E402
from channel_gateway.common.infrastructure.security import JsonCipher  # noqa: E402


class CoreEvents:
    """Only the Core HTTP boundary is replaced; event verification runs unchanged."""
    def __init__(self):
        self.events = []
        self.enabled = True
        self.error = None

    def publish(self, payload, owner='owner'):
        event = {**copy.deepcopy(payload), 'status': 'pending',
                 'notification_id': hashlib.sha256(payload['event_id'].encode()).hexdigest()}
        self.events.append((owner, event))

    def request(self, method, url, **kwargs):
        assert url.startswith('http://core.test.invalid/')
        if self.error:
            raise self.error
        owner = kwargs['headers']['X-User-Id']
        if method == 'POST' and '/task-center/notification-events/' in url and url.endswith(':claim'):
            assert kwargs['headers']['X-LazyMind-Internal-Token'] == 'synthetic-internal-notification-token'
            status = 200 if self.enabled else 409
            return httpx.Response(status, json={'data': {'granted': self.enabled}}, request=httpx.Request(method, url))
        assert method == 'GET'
        if url.endswith('/user/notification-preferences'):
            data = {'enabled': self.enabled}
        elif '/task-center/tasks/' in url and url.endswith('/notifications'):
            task_id = url.split('/tasks/', 1)[1].split('/')[0]
            data = {'items': [event for user, event in self.events if user == owner and event['task_id'] == task_id]}
        else:
            raise AssertionError(f'Unexpected Core request: {url}')
        return httpx.Response(200, json={'data': data}, request=httpx.Request(method, url))


@pytest.fixture
def gateway(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'synthetic-internal-notification-token')
    core_events = CoreEvents()
    monkeypatch.setattr(httpx, 'request', core_events.request)
    driver = os.getenv('CHANNEL_GATEWAY_TEST_DRIVER', 'sqlite')
    schema = 'notification_test_' + uuid.uuid4().hex
    admin_dsn = os.getenv('CHANNEL_GATEWAY_TEST_POSTGRES_DSN')
    if driver == 'postgres':
        if not admin_dsn:
            pytest.fail('PostgreSQL run requires CHANNEL_GATEWAY_TEST_POSTGRES_DSN')
        with psycopg.connect(admin_dsn, autocommit=True) as connection:
            connection.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(schema)))
        dsn = make_conninfo(admin_dsn, options=f'-c search_path={schema}')
    else:
        assert driver == 'sqlite', 'Unsupported test database driver'
        dsn = f'sqlite:///{tmp_path / "gateway.db"}'
    settings = Settings(database_dsn=dsn, credential_key_path=str(tmp_path / 'master.key'),
                        core_base_url='http://core.test.invalid')
    components = build_components(settings)
    try:
        components.store.initialize()
        monkeypatch.setattr(app.state, 'components', components, raising=False)
        # TestClient without a lifespan context does not start network receivers.
        client = TestClient(app, headers={'X-User-Id': 'owner', 'X-Request-Id': 'notification-test'})
        yield SimpleNamespace(store=components.store, components=components, client=client,
                              cipher=JsonCipher(settings.credential_key_path), settings=settings,
                              core_events=core_events)
        client.close()
    finally:
        components.stop()
        if driver == 'postgres':
            with psycopg.connect(admin_dsn, autocommit=True) as connection:
                connection.execute(sql.SQL('DROP SCHEMA {} CASCADE').format(sql.Identifier(schema)))


@pytest.fixture
def account(gateway):
    def create(provider='wechat', owner='owner', identity=None, **extra):
        identity = identity or uuid.uuid4().hex
        secret = uuid.uuid4().hex
        credentials = {'token': secret, 'base_url': 'https://wechat.test.invalid',
                       'account_id': identity, 'authorized_user_id': 'recipient-a',
                       'app_id': identity, 'app_secret': secret, 'provider_account_id': identity,
                       'bot_id': identity, 'secret': secret, **extra}
        row = gateway.store.connect_referenced_account(
            owner_user_id=owner, provider=provider,
            external_id_hash=hashlib.sha256(identity.encode()).hexdigest(),
            label=f'{provider} account', credentials_ciphertext=gateway.cipher.encrypt(owner, credentials),
            status='connected',
        )
        assert row is not None
        return row
    return create


@pytest.fixture
def incoming(gateway):
    def receive(row, recipient='recipient-a', context=None, text='请整理今日简报', message_key=None):
        envelope = InboundEnvelope(
            provider=row['provider'], account_id=row['id'], message_key=message_key or uuid.uuid4().hex,
            order_key=recipient, external_address_hash=hashlib.sha256(recipient.encode()).hexdigest(),
            owner_user_id=row['owner_user_id'], recipient_id=recipient, text=text,
            sensitive_context={'context_token': context} if context else {},
        )
        assert gateway.store.ingest_batch(row['id'], [envelope], None) == 1
        return envelope
    return receive


@pytest.fixture
def queued_reply(gateway, incoming):
    def queue(row, recipient='recipient-a', text='已完成简报', context='test-context'):
        incoming(row, recipient, context)
        inbound = gateway.store.claim_next_inbound('inbound-test', lease_seconds=120)
        assert inbound is not None
        assert gateway.store.complete_inbound(inbound.inbox_id, 'inbound-test', [OutboundMessage(
            provider=row['provider'], account_id=row['id'], order_key=recipient, recipient_id=recipient,
            provider_context={'context_token': context}, text=text, intent_kind='message',
        )], {})
        with gateway.store._connect() as connection:
            return connection.execute('SELECT * FROM channel_outbox WHERE inbox_id = %s',
                                      (inbound.inbox_id,)).fetchone()
    return queue

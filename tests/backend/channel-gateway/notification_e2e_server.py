"""Private subprocess fixture for Core's real HTTP/scheduler/gateway integration test."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import sys
import threading
import uuid

import psycopg
from psycopg import sql
from psycopg.conninfo import make_conninfo
from fastapi.testclient import TestClient

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / 'backend/channel-gateway'))
from channel_gateway.app import app  # noqa: E402
from channel_gateway.bootstrap import Settings, build_components  # noqa: E402
from channel_gateway.common.domain.channel import InboundEnvelope  # noqa: E402
from channel_gateway.common.infrastructure.security import JsonCipher  # noqa: E402
from test_notification_delivery import OneIteration  # noqa: E402
from test_wecom_protocol import BotSocket, ws  # noqa: E402


def main():
    directory, core_url = sys.argv[1:3]
    schema = 'notification_e2e_' + uuid.uuid4().hex
    postgres = os.getenv('CHANNEL_GATEWAY_TEST_DRIVER') == 'postgres'
    admin = os.getenv('CHANNEL_GATEWAY_TEST_POSTGRES_DSN')
    if postgres:
        with psycopg.connect(admin, autocommit=True) as connection:
            connection.execute(sql.SQL('CREATE SCHEMA {}').format(sql.Identifier(schema)))
        dsn = make_conninfo(admin, options=f'-c search_path={schema}')
    else:
        dsn = f'sqlite:///{directory}/gateway.db'
    settings = Settings(database_dsn=dsn, credential_key_path=f'{directory}/master.key', core_base_url=core_url)
    sent, sockets, accounts = [], [], {}
    parts = None
    client = TestClient(app)

    async def socket_connect(_url, **_kwargs):
        socket = BotSocket()
        sockets.append(socket)
        return socket

    ws.websockets.connect = socket_connect

    class Sender:
        def send_markdown(self, **payload):
            sent.append({'provider': 'feishu', 'text': payload['text']})

        def close(self):
            pass

        def send_card(self, **payload):
            sent.append({'provider': 'feishu', 'text': json.dumps(payload['card'], ensure_ascii=False)})
            return 'message-' + uuid.uuid4().hex

    def start():
        nonlocal parts
        parts = build_components(settings)
        parts.store.initialize()
        app.state.components = parts
        registry = parts.delivery_worker._providers
        registry.delivery('wechat')._client.send_text = lambda **payload: sent.append(
            {'provider': 'wechat', 'text': payload['text']})
        registry.delivery('feishu')._channels.create_sender = lambda _credentials: Sender()
        if not accounts:
            cipher = JsonCipher(settings.credential_key_path)
            for provider in ('wechat', 'feishu', 'wecom'):
                identity, secret = uuid.uuid4().hex, uuid.uuid4().hex
                credentials = {'token': secret, 'base_url': 'https://wechat.test.invalid', 'account_id': identity,
                               'authorized_user_id': 'recipient', 'app_id': identity, 'app_secret': secret,
                               'provider_account_id': identity, 'bot_id': identity, 'secret': secret}
                account = parts.store.connect_referenced_account(
                    owner_user_id='owner', provider=provider,
                    external_id_hash=hashlib.sha256(identity.encode()).hexdigest(),
                    label=provider, credentials_ciphertext=cipher.encrypt('owner', credentials), status='connected')
                accounts[provider] = account['id']
                parts.store.ingest_batch(account['id'], [InboundEnvelope(
                    provider=provider, account_id=account['id'], message_key='authorized-message',
                    owner_user_id='owner', recipient_id='recipient', order_key='recipient',
                    external_address_hash='route',
                    text='authorized target', sensitive_context={'context_token': 'synthetic-context'},
                )], None)
        ready = threading.Event()
        original = parts.store.set_runtime_status

        def status(*args, **kwargs):
            result = original(*args, **kwargs)
            if args[1] == 'running':
                ready.set()
            return result

        parts.store.set_runtime_status = status
        registry.delivery('wecom')._runtime.restart_account(accounts['wecom'])
        if not ready.wait(5):
            raise RuntimeError('Fixture SDK authentication timed out')

    class Handler(BaseHTTPRequestHandler):
        drop_once = True

        def handle_request(self):
            raw = self.rfile.read(int(self.headers.get('Content-Length', '0')))
            response = client.request(self.command, self.path, content=raw, headers=dict(self.headers))
            if self.command == 'POST' and self.path.endswith('/task-notifications') and Handler.drop_once:
                Handler.drop_once = False
                self.close_connection = True  # Outbox committed; enqueue acknowledgement is lost.
                return
            self.send_response(response.status_code)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(response.content)

        do_GET = do_POST = do_DELETE = handle_request

        def log_message(self, *_args):
            pass

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    try:
        start()
        threading.Thread(target=server.serve_forever, daemon=True).start()
        print(json.dumps({'url': f'http://127.0.0.1:{server.server_port}', 'accounts': accounts}), flush=True)
        for line in sys.stdin:
            command = json.loads(line)['command']
            if command == 'stop':
                break
            if command == 'restart':
                parts.stop()
                start()
            elif command == 'deliver':
                for _ in range(8):
                    parts.delivery_worker._stop = OneIteration()
                    parts.delivery_worker._run('e2e-delivery')
            messages = sent + [{'provider': 'wecom', 'text': frame['body']['markdown']['content']}
                               for socket in sockets for frame in socket.sent if frame['cmd'] == 'aibot_send_msg']
            print(json.dumps({'sent': messages}), flush=True)
    finally:
        if parts:
            parts.stop()
        client.close()
        server.shutdown()
        server.server_close()
        if postgres:
            with psycopg.connect(admin, autocommit=True) as connection:
                connection.execute(sql.SQL('DROP SCHEMA {} CASCADE').format(sql.Identifier(schema)))


if __name__ == '__main__':
    main()

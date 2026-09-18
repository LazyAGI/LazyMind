"""Personal MCP grants. Database compare-and-swap fences every network operation."""
import base64
import hashlib
import ipaddress
import os
import secrets
import time
from urllib.parse import urlencode, urlsplit

from core.cloud_crypto import decrypt_json, encrypt_json
from core.database import SessionLocal
from models.mcp_oauth import MCPOAuthGrant as Grant
from models.mcp_oauth import MCPOAuthState as State
from sqlalchemy import delete, select, update
from sqlalchemy.exc import IntegrityError

from services.mcp_oauth_http import OAuthError, public_target, request_json


def _callback_url():
    base = os.getenv('LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL', '').rstrip('/')
    try:
        p = urlsplit(base)
        if p.hostname == 'localhost':
            loopback = True
        else:
            try:
                loopback = ipaddress.ip_address(p.hostname or '').is_loopback
            except ValueError:
                loopback = False
        if (not p.hostname or p.username or p.password or p.query or p.fragment or p.path
                or p.port == 0 or any(c.isspace() for c in base)
                or (p.scheme != 'https' and not (p.scheme == 'http' and loopback))):
            raise ValueError()
    except ValueError:
        raise OAuthError('configuration') from None
    return base + '/oauth/mcp/callback'


def _url(url):
    try:
        p = urlsplit(url)
        if (p.scheme != 'https' or not p.hostname or p.username or p.password or p.fragment
                or p.port == 0 or any(c.isspace() for c in url)):
            raise ValueError()
        return p
    except (TypeError, ValueError):
        raise OAuthError('invalid') from None


class MCPOAuthService:
    def __init__(self, sessions=SessionLocal, request=request_json):
        self.sessions = sessions
        self.request = request

    @staticmethod
    def _identity(user_id, server_id, server_url):
        if not user_id or not user_id.strip() or not server_id or not server_id.strip() or not server_url:
            raise OAuthError('invalid')
        _url(server_url)
        return (Grant.user_id == user_id, Grant.server_id == server_id, Grant.server_url == server_url)

    @staticmethod
    def _status(row):
        return {'status': row.status, 'grant_id': row.grant_id, 'grant_version': row.grant_version}

    def status(self, *, user_id, server_id, server_url):
        identity = self._identity(user_id, server_id, server_url)
        with self.sessions() as db:
            row = db.scalar(select(Grant).where(*identity))
            return self._status(row) if row else {
                'status': 'needs_authorization', 'grant_id': None, 'grant_version': None}

    def _discover(self, server_url):
        p = _url(server_url)
        origin = p.scheme + '://' + p.netloc
        urls = [origin + '/.well-known/oauth-protected-resource' + p.path]
        if p.path:
            urls.append(origin + '/.well-known/oauth-protected-resource')
        resource = None
        for url in urls:
            try:
                resource = self.request(url)
                break
            except OAuthError as exc:
                if exc.kind != 'not_found':
                    raise
        if not resource or resource.get('resource') != server_url:
            raise OAuthError('invalid')
        issuers = resource.get('authorization_servers')
        if not isinstance(issuers, list) or not issuers or not isinstance(issuers[0], str):
            raise OAuthError('invalid')
        issuer = issuers[0]
        p = _url(issuer)
        if p.query:
            raise OAuthError('invalid')
        origin = p.scheme + '://' + p.netloc
        path = p.path.rstrip('/')
        urls = [origin + '/.well-known/oauth-authorization-server' + path,
                origin + '/.well-known/openid-configuration' + path]
        if path:
            urls.append(origin + path + '/.well-known/openid-configuration')
        for url in urls:
            try:
                metadata = self.request(url)
                break
            except OAuthError as exc:
                if exc.kind != 'not_found':
                    raise
        else:
            raise OAuthError('not_found')
        if metadata.get('issuer') != issuer or 'S256' not in metadata.get('code_challenge_methods_supported', []):
            raise OAuthError('invalid')
        for key in ('authorization_endpoint', 'token_endpoint', 'registration_endpoint'):
            if not isinstance(metadata.get(key), str):
                raise OAuthError('invalid')
            _url(metadata[key])
            # The authorization endpoint is browser-bound; validate it too.
            if self.request is request_json:
                public_target(metadata[key])
        return metadata, resource.get('scopes_supported', [])

    def authorize(self, *, user_id, server_id, server_url):
        self._identity(user_id, server_id, server_url)
        redirect = _callback_url()
        metadata, scopes = self._discover(server_url)
        client = self.request(metadata['registration_endpoint'], json_body={
            'client_name': 'LazyMind', 'redirect_uris': [redirect],
            'grant_types': ['authorization_code', 'refresh_token'], 'response_types': ['code'],
            'token_endpoint_auth_method': 'none'})
        if (not isinstance(client.get('client_id'), str) or not client['client_id']
                or client.get('token_endpoint_auth_method', 'none') != 'none'):
            raise OAuthError('invalid')
        state, verifier = secrets.token_urlsafe(32), secrets.token_urlsafe(64)
        secret = {'metadata': metadata, 'client_id': client['client_id'], 'redirect_uri': redirect, 'verifier': verifier}
        with self.sessions() as db:
            # Keep one durable row per owner/server; version increment invalidates prior callbacks/refreshes.
            row = db.scalar(select(Grant).where(Grant.user_id == user_id, Grant.server_id == server_id))
            if row:
                grant_id, version = row.grant_id, row.grant_version + 1
                changed = db.execute(update(Grant).where(
                    Grant.grant_id == grant_id, Grant.grant_version == row.grant_version).values(
                    grant_version=version, server_url=server_url, status='pending', ciphertext='', token_version=0,
                    expires_at=0, lease_id='', lease_until=0)).rowcount
                if not changed:
                    raise OAuthError('busy')
            else:
                grant_id, version = secrets.token_hex(24), 1
                db.add(Grant(grant_id=grant_id, user_id=user_id, server_id=server_id, server_url=server_url,
                             grant_version=version, status='pending'))
            db.execute(delete(State).where(State.grant_id == grant_id))
            db.add(State(state_hash=hashlib.sha256(state.encode()).hexdigest(), grant_id=grant_id,
                         grant_version=version, expires_at=time.time() + 600, ciphertext=encrypt_json(secret)))
            try:
                db.commit()
            except IntegrityError:
                raise OAuthError('busy') from None
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
        params = {'response_type': 'code', 'client_id': client['client_id'], 'redirect_uri': redirect,
                  'state': state, 'code_challenge': challenge, 'code_challenge_method': 'S256', 'resource': server_url}
        if scopes and isinstance(scopes, list) and all(isinstance(s, str) for s in scopes):
            params['scope'] = ' '.join(scopes)
        endpoint = metadata['authorization_endpoint']
        return {'authorization_url': endpoint + ('&' if '?' in endpoint else '?') + urlencode(params),
                'status': 'pending', 'grant_id': grant_id, 'grant_version': version}

    @staticmethod
    def _tokens(payload, previous=None):
        if (not isinstance(payload.get('access_token'), str) or not payload['access_token']
                or str(payload.get('token_type', '')).lower() != 'bearer'):
            raise OAuthError()
        try:
            seconds = float(payload.get('expires_in', 300))
            if not 0 < seconds < 10**10:
                raise ValueError()
        except (TypeError, ValueError):
            raise OAuthError() from None
        result = dict(previous or {})
        result['access_token'] = payload['access_token']
        if payload.get('refresh_token'):
            if not isinstance(payload['refresh_token'], str):
                raise OAuthError()
            result['refresh_token'] = payload['refresh_token']
        return result, time.time() + seconds

    def callback(self, *, user_id, server_id, server_url, code, state):
        identity = self._identity(user_id, server_id, server_url)
        if not code or not state:
            raise OAuthError('invalid')
        hashed = hashlib.sha256(state.encode()).hexdigest()
        with self.sessions() as db:
            row = db.scalar(select(Grant).where(*identity))
            tx = db.get(State, hashed)
            if (not row or not tx or tx.grant_id != row.grant_id or tx.grant_version != row.grant_version
                    or tx.expires_at < time.time() or row.status != 'pending'):
                raise OAuthError('invalid')
            grant_id, version = row.grant_id, row.grant_version
            secret = decrypt_json(tx.ciphertext)
            if not db.execute(delete(State).where(State.state_hash == hashed)).rowcount:
                raise OAuthError('invalid')
            db.commit()
        payload = self.request(secret['metadata']['token_endpoint'], data={
            'grant_type': 'authorization_code', 'code': code, 'code_verifier': secret.pop('verifier'),
            'redirect_uri': secret['redirect_uri'], 'client_id': secret['client_id'], 'resource': server_url})
        secret, expiry = self._tokens(payload, secret)
        with self.sessions() as db:
            changed = db.execute(update(Grant).where(
                Grant.grant_id == grant_id, Grant.grant_version == version, Grant.status == 'pending',
            ).values(ciphertext=encrypt_json(secret), expires_at=expiry,
                     token_version=1, status='authorized')).rowcount
            db.commit()
            if not changed:
                raise OAuthError('authorization')
        return {'status': 'authorized', 'grant_id': grant_id, 'grant_version': version}

    def disconnect(self, *, user_id, server_id, server_url):
        identity = self._identity(user_id, server_id, server_url)
        secret = None
        with self.sessions() as db:
            row = db.scalar(select(Grant).where(*identity))
            if row:
                if row.ciphertext:
                    try:
                        secret = decrypt_json(row.ciphertext)
                    except Exception:  # noqa: BLE001 - unreadable secrets must not prevent revocation
                        secret = None  # Corrupt/unreadable secrets must not block local revocation.
                db.execute(update(Grant).where(*identity).values(
                    status='disconnected', ciphertext='', grant_version=Grant.grant_version + 1,
                    lease_id='', lease_until=0, expires_at=0))
                db.execute(delete(State).where(State.grant_id == row.grant_id))
                db.commit()
        if secret and secret.get('metadata', {}).get('revocation_endpoint'):
            try:
                self.request(secret['metadata']['revocation_endpoint'], data={
                    'client_id': secret['client_id'],
                    'token': secret.get('refresh_token') or secret.get('access_token'),
                    'token_type_hint': 'refresh_token' if secret.get('refresh_token') else 'access_token'})
            except Exception:  # noqa: BLE001, S110 - best-effort revocation must not log secrets
                pass  # Local revocation already committed; never restore credentials on provider failure.
        return {'status': 'disconnected', 'grant_id': None, 'grant_version': None}

    def token(self, *, user_id, server_id, server_url, grant_id, grant_version, rejected_token_version=None):
        identity = self._identity(user_id, server_id, server_url) + (
            Grant.grant_id == grant_id, Grant.grant_version == grant_version)
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            with self.sessions() as db:
                row = db.scalar(select(Grant).where(*identity))
                if not row or row.status != 'authorized':
                    raise OAuthError('authorization')
                secret = decrypt_json(row.ciphertext)
                token_version = row.token_version
                # A short-lived token remains usable until expiry. A rejected version
                # must refresh, but concurrent callers can reuse the new version.
                if (row.expires_at > time.time()
                        and (rejected_token_version is None or token_version != rejected_token_version)):
                    return {'status': 'authorized', 'access_token': secret['access_token'],
                            'expires_at': row.expires_at, 'token_version': token_version}
                lease = secrets.token_hex(24)
                # An expired lease is not reclaimed: a crashed refresh may have rotated its token.
                # Reauthorization is safer than concurrent replay of a refresh credential.
                if row.lease_id and row.lease_until <= time.time():
                    db.execute(update(Grant).where(*identity, Grant.lease_id == row.lease_id).values(
                        status='needs_authorization', ciphertext='', lease_id='', lease_until=0))
                    db.commit()
                    raise OAuthError('authorization')
                acquired = db.execute(update(Grant).where(
                    *identity, Grant.status == 'authorized', Grant.token_version == token_version, Grant.lease_id == '',
                ).values(lease_id=lease, lease_until=time.time() + 30)).rowcount
                db.commit()
            if not acquired:
                time.sleep(.05)
                continue
            try:
                if not secret.get('refresh_token'):
                    raise OAuthError('authorization')
                payload = self.request(secret['metadata']['token_endpoint'], data={
                    'grant_type': 'refresh_token', 'refresh_token': secret['refresh_token'],
                    'client_id': secret['client_id'], 'resource': server_url})
                new_secret, expiry = self._tokens(payload, secret)
                with self.sessions() as db:
                    changed = db.execute(update(Grant).where(
                        *identity, Grant.status == 'authorized', Grant.lease_id == lease).values(
                        ciphertext=encrypt_json(new_secret), expires_at=expiry, token_version=token_version + 1,
                        lease_id='', lease_until=0)).rowcount
                    db.commit()
                    if not changed:
                        raise OAuthError('authorization')
                # Recheck durable fences after commit before returning credentials.
                with self.sessions() as db:
                    row = db.scalar(select(Grant).where(
                        *identity, Grant.status == 'authorized', Grant.token_version >= token_version + 1))
                    if not row or row.expires_at <= time.time():
                        raise OAuthError('authorization')
                    current_secret = decrypt_json(row.ciphertext)
                    return {'status': 'authorized', 'access_token': current_secret['access_token'],
                            'expires_at': row.expires_at, 'token_version': row.token_version}
            except OAuthError:
                with self.sessions() as db:
                    # A network failure may have rotated the token; don't replay it automatically.
                    db.execute(update(Grant).where(*identity, Grant.lease_id == lease).values(
                        status='needs_authorization', ciphertext='', lease_id='', lease_until=0))
                    db.commit()
                raise
        raise OAuthError('busy')


mcp_oauth_service = MCPOAuthService()

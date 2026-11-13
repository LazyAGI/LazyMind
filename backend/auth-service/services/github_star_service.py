"""Fixed-project Star operations using the caller's existing GitHub connection."""
import json
import threading
from dataclasses import dataclass
from types import SimpleNamespace
from urllib import request as urllib_request
from urllib.error import HTTPError, URLError

from core.errors import AppException, ErrorCodes
from services.github_cli_runner import GitHubCLIError


REPOSITORY = 'LazyAGI/LazyMind'
_STAR_PATH = '/user/starred/' + REPOSITORY


class _NoRedirect(urllib_request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


# A private HTTP boundary prevents redirects without changing urllib's global opener.
request = SimpleNamespace(urlopen=urllib_request.build_opener(_NoRedirect()).open)


@dataclass
class GitHubStarError(AppException):
    reason: str = 'unavailable'


def _error(error, reason):
    status, code, message = error
    return GitHubStarError(http_code=status, code=code, message=message, reason=reason)


class GitHubStarService:
    def __init__(self, *, connections):
        self.connections = connections
        self._write_lock = threading.RLock()

    @staticmethod
    def _http(path, token, *, method='GET', not_found=False, require_scope=False):
        headers = {
            'Accept': 'application/vnd.github+json', 'Authorization': 'Bearer ' + token,
            'User-Agent': 'LazyMind-Star', 'X-GitHub-Api-Version': '2026-03-10',
        }
        if method == 'PUT':
            headers['Content-Length'] = '0'
        req = urllib_request.Request('https://api.github.com' + path, headers=headers,
                                     method=method, data=b'' if method == 'PUT' else None)
        try:
            with request.urlopen(req, timeout=10) as response:
                if path == _STAR_PATH:
                    if response.status != 204:
                        raise _error(ErrorCodes.GITHUB_STAR_UNAVAILABLE, 'unavailable')
                    return True
                if response.status != 200:
                    raise _error(ErrorCodes.GITHUB_STAR_UNAVAILABLE, 'unavailable')
                if require_scope:
                    scopes = response.headers.get('X-OAuth-Scopes')
                    if scopes is None:
                        raise _error(ErrorCodes.GITHUB_CLI_SCOPE_UNVERIFIED, 'permission_unverified')
                    if not {'repo', 'public_repo'} & set(scopes.replace(',', ' ').split()):
                        raise _error(ErrorCodes.GITHUB_STAR_PERMISSION_REQUIRED, 'permission_denied')
                payload = json.loads(response.read(1 << 20))
                if not isinstance(payload, dict):
                    raise ValueError('invalid GitHub response')
                return payload
        except HTTPError as exc:
            status = exc.code
            headers = exc.headers or {}
            limited = status == 429 or (status == 403 and (
                headers.get('X-RateLimit-Remaining') == '0' or headers.get('Retry-After') is not None))
            if status == 403 and not limited:
                try:
                    message = str(json.loads(exc.read(16_384)).get('message', '')).lower()
                    limited = 'secondary rate limit' in message or 'api rate limit exceeded' in message
                except (ValueError, AttributeError, OSError):
                    pass
            exc.close()
            if limited:
                raise _error((429, *ErrorCodes.GITHUB_STAR_UNAVAILABLE[1:]), 'rate_limited') from None
            if status == 404 and not_found:
                return False
            if status == 401:
                raise _error(ErrorCodes.GITHUB_STAR_AUTH_REQUIRED, 'reauthorize') from None
            if status == 403:
                raise _error(ErrorCodes.GITHUB_STAR_PERMISSION_REQUIRED, 'permission_denied') from None
            raise _error(ErrorCodes.GITHUB_STAR_UNAVAILABLE, 'unavailable') from None
        except (URLError, OSError, ValueError):
            raise _error(ErrorCodes.GITHUB_STAR_UNAVAILABLE, 'unavailable') from None

    def _connection(self, owner, connection_id):
        row = self.connections.get_connection(connection_id, user_id=owner)
        if row['provider'] != 'github' or row['auth_mode'] != 'oauth_user' or row['status'] != 'ACTIVE':
            raise _error(ErrorCodes.GITHUB_STAR_INVALID_CONNECTION, 'reauthorize')
        return row

    def _inspect(self, row, owner):
        try:
            token = self.connections.get_access_token(row['connection_id'], user_id=owner)
        except AppException as exc:
            if exc.http_code == 401:
                raise _error(ErrorCodes.GITHUB_STAR_AUTH_REQUIRED, 'reauthorize') from None
            raise
        if token['provider'] != 'github' or not token.get('access_token'):
            raise _error(ErrorCodes.GITHUB_STAR_INVALID_CONNECTION, 'reauthorize')
        access_token = token['access_token']
        account_id, login = self._inspect_token(access_token, expected_account_id=row['provider_account_id'])
        return access_token, account_id, login

    def _inspect_token(self, access_token, *, expected_account_id=None):
        profile = self._http('/user', access_token, require_scope=True)
        account_id, login = str(profile.get('id') or ''), str(profile.get('login') or '')
        if not account_id or not login or (expected_account_id is not None and account_id != expected_account_id):
            raise _error(ErrorCodes.GITHUB_STAR_ACCOUNT_CHANGED, 'account_changed')
        repository = self._http('/repos/' + REPOSITORY, access_token)
        if str(repository.get('full_name') or '').lower() != REPOSITORY.lower():
            raise _error(ErrorCodes.GITHUB_STAR_UNAVAILABLE, 'unavailable')
        return account_id, login

    def _local_identity(self, probe):
        if probe is None:
            return None
        try:
            credential = probe()
        except GitHubCLIError:
            return None
        if credential is None:
            return None
        token = credential['access_token']
        try:
            account_id, login = self._inspect_token(token)
        except AppException as exc:
            if exc.http_code in (401, 403):
                return None
            raise
        return token, account_id, login

    def status(self, *, owner_user_id: str, connection_id: str = '') -> dict:
        if connection_id:
            # Ownership errors are not disguised as an empty account list.
            rows = [self.connections.get_connection(connection_id, user_id=owner_user_id)]
        else:
            rows = self.connections.list_connections(owner_user_id=owner_user_id, provider='github')['items']
        accounts = []
        for row in rows:
            if row['status'] == 'REVOKED':
                continue
            account = {'connection_id': row['connection_id'], 'account_id': row['provider_account_id'],
                       'login': (row.get('provider_account_meta') or {}).get('login') or row['display_name'],
                       'state': 'unknown', 'eligible': False}
            try:
                row = self._connection(owner_user_id, row['connection_id'])
                token, account_id, login = self._inspect(row, owner_user_id)
                starred = self._http(_STAR_PATH, token, not_found=True)
                account.update(account_id=account_id, login=login,
                               state='starred' if starred else 'not_starred', eligible=True)
            except AppException:
                pass  # Unknown/unauthorized accounts cannot trigger an invitation.
            accounts.append(account)
        return {'repository': REPOSITORY, 'accounts': accounts}

    def star(self, *, owner_user_id: str, connection_id: str,
             expected_account_id: str, confirmed: bool) -> dict:
        if confirmed is not True:
            raise _error(ErrorCodes.INVALID_REQUEST, 'confirmation_required')
        with self._write_lock:
            row = self._connection(owner_user_id, connection_id)
            if expected_account_id != row['provider_account_id']:
                raise _error(ErrorCodes.GITHUB_STAR_ACCOUNT_CHANGED, 'account_changed')
            token, account_id, login = self._inspect(row, owner_user_id)
            result = {'repository': REPOSITORY, 'connection_id': connection_id,
                      'account_id': account_id, 'login': login, 'state': 'starred', 'write_performed': False}
            return self._write_star(token, result)

    def local_capability(self, *, probe=None) -> dict:
        return {'repository': REPOSITORY, 'eligible': self._local_identity(probe) is not None}

    def star_local(self, *, confirmed: bool, probe=None) -> dict:
        if confirmed is not True:
            raise _error(ErrorCodes.INVALID_REQUEST, 'confirmation_required')
        unavailable = {'repository': REPOSITORY, 'state': 'connection_required',
                       'account_id': '', 'login': '', 'write_performed': False}
        if probe is None:
            return unavailable
        with self._write_lock:
            try:
                identity = self._local_identity(probe)
                if identity is None:
                    return unavailable
                token, account_id, login = identity
                result = {**unavailable, 'state': 'starred', 'account_id': account_id, 'login': login}
                return self._write_star(token, result)
            except AppException as exc:
                if exc.http_code in (401, 403):
                    return unavailable
                raise

    def _write_star(self, token, result):
        if self._http(_STAR_PATH, token, not_found=True):
            return result
        try:
            self._http(_STAR_PATH, token, method='PUT')
        except AppException as exc:
            if exc.http_code in (401, 403, 429):
                raise
            # A lost response may follow a committed write. Never replay PUT.
        try:
            if self._http(_STAR_PATH, token, not_found=True):
                return {**result, 'write_performed': True}
        except AppException:
            pass
        raise _error(ErrorCodes.GITHUB_STAR_RESULT_UNKNOWN, 'result_unknown')

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping
from typing import Any, NamedTuple


REPEATED_FAILURE_LIMIT = 3
FATAL_HTTP_REASONS = {
    401: 'opencode_auth_failed',
    402: 'opencode_balance_exhausted',
    403: 'opencode_permission_denied',
}
HTTP_STATUS = re.compile(r'(?<!\d)(401|402|403|429|5\d\d)(?!\d)')
FATAL_ERROR_TOKENS = {
    'authentication_failed': 'opencode_auth_failed',
    'permission_denied': 'opencode_permission_denied',
    'balance_exhausted': 'opencode_balance_exhausted',
    'quota_exhausted': 'opencode_quota_exhausted',
}


class FailureDecision(NamedTuple):
    retryable: bool
    reason_code: str
    fingerprint: str


def classify_worker_failure(error: object) -> FailureDecision:
    text = _canonical_text(error)
    codes = _http_codes(error, text)
    for code, reason in FATAL_HTTP_REASONS.items():
        if code in codes:
            return FailureDecision(False, reason, _fingerprint(reason, text))
    casefolded = text.casefold()
    for token, reason in FATAL_ERROR_TOKENS.items():
        if token in casefolded:
            return FailureDecision(False, reason, _fingerprint(reason, text))
    if 429 in codes:
        reason = 'opencode_rate_limited'
    elif any(500 <= code <= 599 for code in codes):
        reason = 'opencode_provider_unavailable'
    elif 'timeout' in casefolded or 'timed out' in casefolded:
        reason = 'opencode_timeout'
    elif 'configuration_error' in casefolded:
        return FailureDecision(False, 'opencode_configuration_error', _fingerprint('config', text))
    else:
        reason = 'opencode_worker_failed'
    return FailureDecision(True, reason, _fingerprint(reason, text))


def attempt_failure_fingerprint(worker_failure: str, candidate_reason: object, diff: object) -> str:
    diff_hash = hashlib.sha1(str(diff or '').encode()).hexdigest()[:12]
    return _fingerprint(worker_failure, str(candidate_reason or ''), diff_hash)


def _canonical_text(value: object) -> str:
    try:
        return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    except (TypeError, ValueError):
        return str(value)


def _http_codes(value: object, text: str) -> set[int]:
    codes = {int(match) for match in HTTP_STATUS.findall(text)}

    def walk(item: Any) -> None:
        if isinstance(item, Mapping):
            for key, child in item.items():
                if str(key).casefold() in {'status', 'statuscode', 'status_code', 'httpstatus'}:
                    try:
                        code = int(child)
                    except (TypeError, ValueError):
                        pass
                    else:
                        if 100 <= code <= 599:
                            codes.add(code)
                walk(child)
        elif isinstance(item, (list, tuple)):
            for child in item:
                walk(child)

    walk(value)
    return codes


def _fingerprint(*parts: str) -> str:
    return hashlib.sha1('\x1f'.join(parts).encode()).hexdigest()[:16]

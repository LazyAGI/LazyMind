from __future__ import annotations

MAIL_IMAP_PROVIDERS = ('qqmail', 'qqexmail', 'netease163', 'neteaseqiye', 'gmailimap')
MAIL_PROVIDERS = MAIL_IMAP_PROVIDERS

_DEFAULT_IMAP_PORT = 993
_DEFAULT_SMTP_PORT = 465

# One IMAP family per product. Personal NetEase/QQ map hosts by domain;
# enterprise and Gmail IMAP accept custom domains and use a fixed host.
IMAP_ENDPOINTS = {
    'netease163': {
        'imap_id': True,
        'by_domain': {
            '163.com': {'imap_host': 'imap.163.com', 'smtp_host': 'smtp.163.com'},
            '126.com': {'imap_host': 'imap.126.com', 'smtp_host': 'smtp.126.com'},
            'yeah.net': {'imap_host': 'imap.yeah.net', 'smtp_host': 'smtp.yeah.net'},
            'vip.163.com': {'imap_host': 'imap.vip.163.com', 'smtp_host': 'smtp.vip.163.com'},
            '188.com': {'imap_host': 'imap.188.com', 'smtp_host': 'smtp.188.com'},
        },
    },
    'qqmail': {
        'by_domain': {
            'qq.com': {'imap_host': 'imap.qq.com', 'smtp_host': 'smtp.qq.com'},
            'foxmail.com': {'imap_host': 'imap.qq.com', 'smtp_host': 'smtp.qq.com'},
        },
    },
    'qqexmail': {
        'imap_host': 'imap.exmail.qq.com',
        'smtp_host': 'smtp.exmail.qq.com',
    },
    'neteaseqiye': {
        'imap_id': True,
        'imap_host': 'imap.qiye.163.com',
        'smtp_host': 'smtp.qiye.163.com',
    },
    'gmailimap': {
        'imap_host': 'imap.gmail.com',
        'smtp_host': 'smtp.gmail.com',
    },
}


def is_mail_provider(provider: str) -> bool:
    return (provider or '').strip().lower() in MAIL_PROVIDERS


def is_mail_imap_provider(provider: str) -> bool:
    return (provider or '').strip().lower() in MAIL_IMAP_PROVIDERS


def _match_domain_hosts(by_domain: dict[str, dict[str, str]], domain: str) -> dict[str, str] | None:
    for suffix in sorted(by_domain, key=len, reverse=True):
        if domain == suffix or domain.endswith('.' + suffix):
            return by_domain[suffix]
    return None


def resolve_imap_endpoint(provider: str, email: str) -> dict[str, object]:
    key = (provider or '').strip().lower()
    spec = IMAP_ENDPOINTS.get(key)
    if spec is None:
        raise RuntimeError(f'unsupported imap mail provider: {provider}')
    address = (email or '').strip()
    domain = address.rsplit('@', 1)[-1].lower() if '@' in address else ''
    by_domain = spec.get('by_domain') or {}
    hosts = _match_domain_hosts(by_domain, domain) if by_domain else None
    if by_domain and hosts is None:
        allowed = ', '.join(sorted(by_domain, key=len, reverse=True))
        raise RuntimeError(f'{key} only supports {allowed} addresses')
    imap_host = (hosts or {}).get('imap_host') or spec.get('imap_host')
    smtp_host = (hosts or {}).get('smtp_host') or spec.get('smtp_host')
    if not imap_host or not smtp_host:
        raise RuntimeError(f'{key} is missing IMAP/SMTP hosts')
    return {
        'imap_host': imap_host,
        'imap_port': int(spec.get('imap_port') or _DEFAULT_IMAP_PORT),
        'smtp_host': smtp_host,
        'smtp_port': int(spec.get('smtp_port') or _DEFAULT_SMTP_PORT),
        'imap_id': bool(spec.get('imap_id')),
    }

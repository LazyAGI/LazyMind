import re

_ENV_NAME_RE = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*$')
_CONTROL_ENV_NAME_RE = re.compile(r'^(LD_|DYLD_)', re.IGNORECASE)
_BLOCKED_ENV_NAMES = {
    'HOME',
    'PATH',
    'PYTHONPATH',
    'PYTHONHOME',
    'PYTHONSTARTUP',
    'PYTHONEXECUTABLE',
    'PYTHONINSPECT',
    'PYTHONBREAKPOINT',
    'LD_LIBRARY_PATH',
    'LD_PRELOAD',
    'DYLD_LIBRARY_PATH',
    'DYLD_INSERT_LIBRARIES',
    'SHELL',
    'PWD',
    'IFS',
    'ENV',
    'BASH_ENV',
    'HTTP_PROXY',
    'HTTPS_PROXY',
    'ALL_PROXY',
    'NO_PROXY',
    'FTP_PROXY',
    'SSL_CERT_FILE',
    'SSL_CERT_DIR',
    'REQUESTS_CA_BUNDLE',
    'CURL_CA_BUNDLE',
    'SSLKEYLOGFILE',
    'NODE_OPTIONS',
    'NODE_EXTRA_CA_CERTS',
    'NODE_PATH',
    'NODE_TLS_REJECT_UNAUTHORIZED',
    'OPENSSL_CONF',
    'OPENSSL_MODULES',
    'RUBYOPT',
    'RUBYLIB',
    'PERL5OPT',
    'PERL5LIB',
    'GIT_CONFIG',
    'GIT_CONFIG_GLOBAL',
    'GIT_CONFIG_SYSTEM',
    'GIT_CONFIG_COUNT',
    'GIT_CONFIG_PARAMETERS',
    'GIT_SSL_NO_VERIFY',
    'GIT_SSL_CAINFO',
    'GIT_SSL_CAPATH',
    'GIT_SSH_COMMAND',
    'ZDOTDIR',
    'PROMPT_COMMAND',
}


def validate_env_name(name: str) -> str:
    cleaned = str(name or '').strip()
    if not cleaned:
        raise ValueError('env name is required')
    if len(cleaned) > 128 or not _ENV_NAME_RE.fullmatch(cleaned):
        raise ValueError('env name must match ^[A-Za-z_][A-Za-z0-9_]*$')
    if cleaned.upper() in _BLOCKED_ENV_NAMES:
        raise ValueError(f'env name {cleaned!r} is reserved and cannot be changed from chat')
    if _CONTROL_ENV_NAME_RE.search(cleaned):
        raise ValueError(f'env name {cleaned!r} controls runtime behavior and cannot be changed from chat')
    return cleaned

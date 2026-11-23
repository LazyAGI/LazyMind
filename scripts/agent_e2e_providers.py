"""Private provider configuration for CI; public reports never contain credentials."""
from __future__ import annotations

import base64
import hashlib
import io
import json
import os
import re
import secrets
import stat
import threading
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_PATH = Path.home() / '.config/lazymind-ci/providers.json'
CONFIG_HINT = ('请复制 scripts/agent_e2e.providers.example.json 到仓库外的 '
               '~/.config/lazymind-ci/providers.json，填写自己的模型配置并 chmod 600；'
               'GitHub 上使用 Secrets 注入 LAZYMIND_CI_LLM_API_KEY / LAZYMIND_CI_IMAGE_API_KEY，勿提交密钥。')


class ProviderConfigurationError(RuntimeError):
    pass


def endpoint(value):
    try:
        parsed = urllib.parse.urlsplit(value)
        local = parsed.hostname in ('localhost', '127.0.0.1', '::1')
        if (parsed.scheme != 'https' and not (parsed.scheme == 'http' and local)
                or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment):
            raise ValueError
    except (TypeError, ValueError):
        raise ProviderConfigurationError('模型 API 地址须为不含密钥、查询参数的 HTTPS 地址（本机可用 HTTP）。') from None
    return value.rstrip('/')


def load(path=None, *, required=False, require_image=False):
    path = Path(path or os.environ.get('LAZYMIND_CI_PROVIDERS_CONFIG') or DEFAULT_PATH).expanduser().resolve()
    data = {}
    if path.is_file():
        try:
            if path.stat().st_size > 1024 * 1024:
                raise ValueError
            data = json.loads(path.read_text(encoding='utf-8'))
            if not isinstance(data, dict) or data.get('version') != 1:
                raise ValueError
        except (OSError, ValueError):
            raise ProviderConfigurationError('私有配置格式无效。' + CONFIG_HINT) from None
        direct_keys = any(isinstance(data.get(n), dict) and data[n].get('api_key') for n in ('llm', 'image'))
        if direct_keys:
            if path.is_relative_to(ROOT):
                raise ProviderConfigurationError('含密钥的配置须保存在仓库外。' + CONFIG_HINT)
            if os.name == 'posix' and stat.S_IMODE(path.stat().st_mode) & 0o077:
                raise ProviderConfigurationError('私有配置权限过宽，请 chmod 600。')
    elif not any(os.environ.get('LAZYMIND_CI_' + n + '_API_KEY') for n in ('LLM', 'IMAGE')):
        if required:
            raise ProviderConfigurationError('缺少 CI 模型配置。' + CONFIG_HINT)
        return None
    result = {'path': str(path), 'version': 1}
    for name in ('llm', 'image'):
        section = data.get(name) or {}
        if not isinstance(section, dict):
            raise ProviderConfigurationError('模型配置项必须是 JSON 对象。' + CONFIG_HINT)
        prefix = 'LAZYMIND_CI_' + name.upper() + '_'
        key_env = section.get('api_key_env') or prefix + 'API_KEY'
        if not isinstance(key_env, str) or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key_env):
            raise ProviderConfigurationError('api_key_env 须填写环境变量名称。')
        key = os.environ.get(key_env) or section.get('api_key') or ''
        base = os.environ.get(prefix + 'BASE_URL') or section.get('base_url') or ''
        model = os.environ.get(prefix + 'MODEL') or section.get('model') or ''
        if name == 'image' and not key and not require_image:
            continue
        missing = [field for field, value in (('api_key', key), ('base_url', base), ('model', model)) if not isinstance(value, str) or not value.strip()]
        if missing:
            raise ProviderConfigurationError(name + ' 缺少配置字段：' + ', '.join(missing) + '。' + CONFIG_HINT)
        base = endpoint(base)
        api = os.environ.get(prefix + 'API') or section.get('api') or ('siliconflow-images' if name == 'image' and 'siliconflow.cn' in urllib.parse.urlsplit(base).hostname else
                                     'openai-images' if name == 'image' else 'openai-compatible')
        if api not in ({'openai-compatible', 'openai-responses'} if name == 'llm' else {'openai-images', 'siliconflow-images'}):
            raise ProviderConfigurationError('不支持的模型 API 协议。')
        result[name] = {'api_key': key.strip(), 'base_url': endpoint(base), 'model': model.strip(), 'api': api}
    return result


def public(config):
    return {name: {k: row[k] for k in ('base_url', 'model', 'api')}
            for name, row in config.items() if name in ('llm', 'image')}


def fingerprint(config):
    # Pins credential rotation too; only the irreversible digest enters reports.
    return hashlib.sha256(json.dumps(config, sort_keys=True).encode()).hexdigest()


def workbuddy_models(config, *, proxy=False):
    row = config['llm']
    base = row['base_url'].removesuffix('/chat/completions').removesuffix('/responses')
    if proxy:
        base = '${CODEBUDDY_BASE_URL}'
    model = {'id': row['model'], 'name': 'CI supplied model', 'vendor': 'CI',
             'url': base if row['api'] == 'openai-responses' else base + '/chat/completions',
             'apiKey': '${CODEBUDDY_API_KEY}', 'maxInputTokens': 128000, 'maxOutputTokens': 8192,
             'supportsToolCall': True, 'supportsImages': True}
    if row['api'] == 'openai-responses':
        model['api'] = row['api']
    return {'models': [model], 'availableModels': [row['model']]}


def workbuddy_usage(value):
    """The bundled CLI expects numeric usage details; omit the new TTL breakdown only."""
    response = value.get('response', value) if isinstance(value, dict) else {}
    usage = response.get('usage') if isinstance(response, dict) else None
    details = usage.get('input_tokens_details') if isinstance(usage, dict) else None
    if isinstance(details, dict) and isinstance(details.get('cache_write_tokens_details'), dict):
        del details['cache_write_tokens_details']
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class ModelProxy:
    """Own provider credentials; the Agent receives only a transient loopback token."""
    def __init__(self, config, ppt_export=None):
        self.token = secrets.token_urlsafe(32)
        self.config = config
        self.diagnostics = []
        self.ppt_export = ppt_export
        owner = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def reject(self, status, code):
                raw = json.dumps({'error': {'message': code, 'code': code}}).encode()
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def do_POST(self):
                if self.headers.get('Authorization') != 'Bearer ' + owner.token:
                    self.reject(401, 'ci_proxy_authentication_required')
                    return
                route = self.path.removeprefix('/v1')
                if route == '/ci/ppt-export' and owner.ppt_export:
                    try:
                        length = int(self.headers.get('Content-Length', '0'))
                        if not 0 < length <= 4096:
                            raise ValueError('invalid_export_request')
                        result = owner.ppt_export(json.loads(self.rfile.read(length)))
                        raw = json.dumps(result, ensure_ascii=False).encode()
                        self.send_response(200)
                        self.send_header('Content-Type', 'application/json')
                        self.send_header('Content-Length', str(len(raw)))
                        self.end_headers()
                        self.wfile.write(raw)
                    except (OSError, ValueError, RuntimeError):
                        self.reject(400, 'ci_ppt_export_rejected')
                    return
                name = 'image' if route == '/images/generations' else 'llm'
                if route not in ('/chat/completions', '/responses', '/images/generations') or name not in owner.config:
                    self.reject(404, 'ci_proxy_route_unavailable')
                    return
                row = owner.config[name]
                try:
                    length = int(self.headers.get('Content-Length', '0'))
                    if not 0 < length <= 16 * 1024 * 1024:
                        raise ValueError
                    raw = self.rfile.read(length)
                    body = json.loads(raw)
                    if body.get('model') != row['model']:
                        self.reject(400, 'ci_proxy_model_mismatch')
                        return
                    base = row['base_url'].removesuffix('/chat/completions').removesuffix('/responses').removesuffix('/images/generations')
                    request = urllib.request.Request(base + route, data=raw, headers={
                        'Authorization': 'Bearer ' + row['api_key'], 'Content-Type': 'application/json', 'Accept-Encoding': 'identity'})
                    with urllib.request.build_opener(NoRedirect).open(request, timeout=180) as response:
                        self.send_response(response.status)
                        content_type = response.headers.get('Content-Type', 'application/json')
                        self.send_header('Content-Type', content_type)
                        self.send_header('Connection', 'close')
                        self.end_headers()
                        self.close_connection = True
                        if route == '/responses' and 'text/event-stream' in content_type:
                            while line := response.readline(16 * 1024 * 1024):
                                if line.startswith(b'data: ') and line.strip() != b'data: [DONE]':
                                    try:
                                        line = b'data: ' + json.dumps(workbuddy_usage(json.loads(line[6:])), ensure_ascii=False).encode() + b'\n'
                                    except ValueError:
                                        pass
                                self.wfile.write(line)
                                self.wfile.flush()
                        elif route == '/responses' and 'application/json' in content_type:
                            self.wfile.write(json.dumps(workbuddy_usage(json.load(response)), ensure_ascii=False).encode())
                        else:
                            while chunk := response.read1(65536):
                                self.wfile.write(chunk)
                                self.wfile.flush()
                except urllib.error.HTTPError as error:
                    diagnostic = {'status': error.code, 'route': route}
                    try:
                        detail = json.load(error).get('error', {})
                        for field in ('code', 'param', 'type'):
                            value = detail.get(field)
                            if (isinstance(value, str) and row['api_key'] not in value
                                    and re.fullmatch(r'[A-Za-z0-9_.\[\]-]{1,80}', value)):
                                diagnostic[field] = value
                    except (ValueError, AttributeError):
                        pass
                    owner.diagnostics.append(diagnostic)
                    self.reject(error.code, 'provider_http_' + str(error.code) + '_' + diagnostic.get('param', 'request'))
                except (BrokenPipeError, ConnectionResetError):
                    return
                except (OSError, ValueError):
                    self.reject(502, 'ci_proxy_provider_request_failed')
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.server.daemon_threads = True
        self.base_url = 'http://127.0.0.1:' + str(self.server.server_port) + '/v1'

    def __enter__(self):
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)


def request_json(row, suffix, payload, timeout=90):
    base = row['base_url'].removesuffix('/chat/completions').removesuffix('/responses').removesuffix('/images/generations')
    request = urllib.request.Request(base + suffix, data=json.dumps(payload).encode(),
                 headers={'Authorization': 'Bearer ' + row['api_key'], 'Content-Type': 'application/json'})
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=timeout) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        # Provider errors sometimes echo credentials. Never return their bodies.
        raise ProviderConfigurationError(f'模型接口返回 HTTP {error.code}；请检查端点、Key 权限、模型及额度。') from None


def generate_image(config, prompt, output, size=512):
    from PIL import Image
    if 'image' not in config:
        raise ProviderConfigurationError('未配置生图模型。' + CONFIG_HINT)
    output = Path(output).resolve()
    if not output.is_relative_to(Path.cwd().resolve()):
        raise ProviderConfigurationError('生图输出必须位于本次测试工作目录内。')
    row = config['image']
    payload = {'model': row['model'], 'prompt': prompt}
    if row['api'] == 'siliconflow-images':
        payload.update(image_size='1024x1024', num_inference_steps=20)
    else:
        payload.update(size='1024x1024', n=1)
    value = request_json(row, '/images/generations', payload)
    files = value.get('images') or value.get('data') or []
    if not files:
        raise ProviderConfigurationError('生图接口未返回图片。')
    item = files[0]
    if item.get('b64_json'):
        raw = base64.b64decode(item['b64_json'], validate=True)
    else:
        url = item.get('url') or ''
        if not url.startswith('https://'):
            raise ProviderConfigurationError('生图结果未提供 HTTPS 文件。')
        # The storage host receives no provider Authorization header.
        with urllib.request.build_opener(NoRedirect).open(url, timeout=60) as response:
            raw = response.read(24 * 1024 * 1024 + 1)
    if len(raw) > 24 * 1024 * 1024:
        raise ProviderConfigurationError('图片超出 24 MiB 传输上限。')
    with Image.open(io.BytesIO(raw)) as image:
        original = list(image.size)
        if image.width != image.height or getattr(image, 'n_frames', 1) != 1:
            raise ProviderConfigurationError('本次静态表情用例要求单帧方图。')
        image.load()
        if image.size != (size, size):
            image = image.resize((size, size), Image.Resampling.LANCZOS)
        output.parent.mkdir(parents=True, exist_ok=True)
        image.save(output, format='PNG')
    return {'local_path': str(output), 'width': size, 'height': size, 'original_size': original,
            'model': row['model'], 'sha256': hashlib.sha256(output.read_bytes()).hexdigest()}

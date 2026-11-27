"""Best-effort Markdown image dependencies; original host paths never become Runtime read authority."""
import base64
import hashlib
import json
import re
import time
from contextvars import copy_context
from pathlib import Path
from queue import Empty, Queue
from threading import Thread
from urllib.parse import unquote

from lazyllm.thirdparty import mistune

_MARKER = re.compile(r'\n\n<!-- lazymind-markdown-images:v1:([A-Za-z0-9+/=]{1,65536}) -->\s*$')
_RESOURCE_ID = re.compile(r'^[A-Za-z0-9_-]{1,128}$')
MAX_IMAGE_BYTES = 20 << 20


def unpack_markdown(text):
    """Keep the exact source body and remove the bounded transport-only dependency manifest."""
    match = _MARKER.search(text)
    if not match:
        return text, {}, []
    body = text[:match.start()]
    try:
        manifest = json.loads(base64.b64decode(match[1], validate=True))
        if not isinstance(manifest, dict) or len(manifest.get('images', [])) > 32:
            raise ValueError('invalid image manifest')
        images = {}
        for item in manifest.get('images', []):
            source, identifier = item['source'], item['resource_id']
            if not isinstance(source, str) or len(source) > 4096 or not _RESOURCE_ID.fullmatch(identifier):
                raise ValueError('invalid image dependency')
            images[source] = identifier
        warnings = [str(value)[:500] for value in manifest.get('warnings', [])[:32]]
        return body, images, warnings
    except (ValueError, TypeError, KeyError):
        return body, {}, ['图片依赖信息无效，已跳过图片收集。']


def image_sources(text):
    pending = list(mistune.create_markdown(renderer='ast')(text))
    sources = set()
    while pending:
        node = pending.pop()
        if node.get('type') == 'image':
            sources.add(str((node.get('attrs') or {}).get('url') or ''))
        pending.extend(node.get('children') or [])
    return sources


def read_image_resource(identifier, timeout):
    # Same authenticated Core boundary as other document tools, with a smaller image-only timeout.
    import requests
    from lazymind.config import config
    from lazymind.chat.engine.tools.infra.core_api_client import _current_user_headers

    with requests.Session() as session:
        session.trust_env = False
        response = session.get(str(config['core_api_url']).rstrip('/') + '/workflow-input-resources/' + identifier,
                               headers={**_current_user_headers(), 'Workflow-Contract-Version': 'workflow.v1'},
                               timeout=timeout)
        response.raise_for_status()
        value = response.json()
    return value.get('result', value)


def _bounded_read(read_resource, identifier, timeout):
    result = Queue(maxsize=1)

    def read():
        try:
            result.put(read_resource(identifier, timeout))
        except Exception as exc:
            result.put(exc)

    # A late network result may finish, but can never write files or alter a frozen library.
    request_context = copy_context()
    Thread(target=lambda: request_context.run(read), daemon=True).start()
    try:
        value = result.get(timeout=timeout)
    except Empty as exc:
        raise TimeoutError('image resource read timed out') from exc
    if isinstance(value, Exception):
        raise value
    return value


def prepare_markdown_inputs(paths, directory, *, read_resource=None, budget=None):
    """Stage only owned image resources; failures never make the document unavailable."""
    read_resource = read_resource or read_image_resource
    documents, resources, warnings, cache = [], [], [], {}
    pending = []
    for path in paths:
        source_path = Path(path)
        if source_path.suffix.lower() not in {'.md', '.markdown'}:
            continue
        try:
            text, dependencies, notices = unpack_markdown(source_path.read_text(encoding='utf-8'))
            references = image_sources(text)
        except Exception as exc:
            warnings.append(f'{source_path.name}：无法读取图片引用（{type(exc).__name__}），已跳过。')
            continue
        warnings.extend(notices)
        document = {'path': str(source_path), 'text': text, 'images': {}}
        documents.append(document)
        pending.append((source_path, document, dependencies, references))
    if budget is None:
        budget = max(30.0, min(180.0, sum(len(item[3]) for item in pending) * 10.0))
    deadline = time.monotonic() + budget
    for source_path, document, dependencies, references in pending:
        for reference in sorted(references):
            identifier = dependencies.get(reference) or next(
                (value for key, value in dependencies.items() if unquote(key) == unquote(reference)), None)
            if not identifier:
                if not re.match(r'^(?:https?:|data:|asset:|//)', reference, re.I):
                    warnings.append(f'{source_path.name}：图片未导入：{reference}')
                continue
            try:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('image collection budget exhausted')
                if identifier not in cache:
                    resource = _bounded_read(read_resource, identifier, min(5.0, remaining))
                    data = base64.b64decode(resource['content_base64'], validate=True)
                    digest = hashlib.sha256(data).hexdigest()
                    if (not 0 < len(data) <= MAX_IMAGE_BYTES or resource.get('content_hash') != 'sha256:' + digest
                            or not str(resource.get('mime_type', '')).startswith('image/')):
                        raise ValueError('invalid image resource')
                    suffix = Path(resource.get('name', '')).suffix.lower()
                    if suffix not in {'.png', '.jpg', '.jpeg', '.gif', '.webp', '.svg', '.bmp', '.tif', '.tiff'}:
                        raise ValueError('unsupported image format')
                    target = Path(directory) / 'inputs' / (digest + suffix)
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(data)
                    cache[identifier] = (str(target), digest)
                    resources.append({'resource_id': 'markdown-image-' + identifier, 'resource_type': 'image',
                                      'uri': str(target), 'title': resource.get('name'),
                                      'meta': {'origin': 'markdown', 'source_reference': reference}})
                document['images'][reference] = cache[identifier][1]
            except Exception as exc:
                warnings.append(f'{source_path.name}：图片未导入：{reference}（{type(exc).__name__}）')
    return documents, resources, warnings


def save_source_materials(documents, library, directory):
    """Publish exact Markdown bodies separately; scope reference aliases to each document."""
    paths, warnings = [], []
    for index, document in enumerate(documents):
        try:
            root = Path(directory) / str(index)
            root.mkdir(parents=True, exist_ok=True)
            path = root / Path(document['path']).name
            path.write_text(document['text'], encoding='utf-8')
            paths.append(str(path))
            for reference, digest in document['images'].items():
                asset = (library.get('assets') or {}).get('asset-' + digest)
                if asset:
                    aliases = asset.setdefault('meta', {}).setdefault('markdown_source_references', {})
                    aliases.setdefault(str(len(paths) - 1), []).append(reference)
        except Exception as exc:
            warnings.append(f'材料展示副本保存失败（{type(exc).__name__}），写作继续。')
    return paths, warnings

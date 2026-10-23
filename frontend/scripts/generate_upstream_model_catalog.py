#!/usr/bin/env python3
"""Fetch the model catalog from models.dev and OpenRouter at build time.

The JSON is not checked in. Builds share a validated snapshot for 24 hours;
runtime startup never needs to contact the public catalogs. Service availability
is limited to supported providers; a separate index recognizes model types from
all upstream suppliers without adding their endpoints or offered models.
"""

import argparse
import http.client
import json
import os
import sys
import tempfile
import time
import urllib.request
from datetime import datetime, timezone


def find_root():
    current = os.path.abspath(os.path.dirname(__file__))
    while current != os.path.dirname(current):
        if os.path.isfile(os.path.join(current, 'Makefile')) and os.path.isdir(os.path.join(current, 'backend')):
            return current
        current = os.path.dirname(current)
    return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))


ROOT = find_root()
MODELS_DEV_URL = 'https://models.dev/api.json?type=all'
OPENROUTER_URL = 'https://openrouter.ai/api/v1/models?output_modalities=all'
SCHEMA_VERSION = 3

PROVIDER_SOURCES = {
    'Claude': ['anthropic'],
    'DeepSeek': ['deepseek'],
    'Doubao': ['volcengine'],
    'GLM': ['zhipuai', 'zai'],
    'Kimi': ['moonshotai-cn', 'moonshotai'],
    'Minimax': ['minimax-cn', 'minimax'],
    'OpenAI': ['openai'],
    'Qwen': ['alibaba-cn', 'alibaba'],
    'SenseNova': ['sensenova'],
    'SiliconFlow': ['siliconflow-cn', 'siliconflow'],
}


def fetch_json(url):
    request = urllib.request.Request(url, headers={'Accept': 'application/json', 'User-Agent': 'LazyMind-catalog'})
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return json.load(response)
        except (OSError, http.client.HTTPException):
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def normalize_modalities(values):
    # Retain upstream task modalities, including future ones. Collapsing them
    # into text/image/audio would lose the distinction between speech, music,
    # transcription, embeddings, decisions and ordinary chat.
    found = []
    for value in values if isinstance(values, list) else []:
        if isinstance(value, str) and (normalized := value.strip().lower()) and normalized not in found:
            found.append(normalized)
    return found


def blank_model(name):
    return {'name': name, 'model_type': '', 'input': [], 'output': [], 'context': 0, 'max_input': 0, 'max_output': 0}


def model_type(kind, model):
    inputs, outputs = set(model['input']), set(model['output'])
    if outputs - {'text', 'image', 'audio', 'video', 'speech', 'transcription', 'embedding', 'embeddings', 'rerank'}:
        return ''
    declared = str(kind or '').strip().lower()
    if declared:
        if declared in {'embedding', 'embeddings', 'embed'}:
            return 'multimodal_embedding' if inputs - {'text'} else 'embed'
        return {
            'chat': 'llm', 'llm': 'llm', 'vlm': 'vlm',
            'reranking': 'rerank', 'rerank': 'rerank',
            'transcription': 'stt', 'stt': 'stt', 'asr': 'stt',
            'speech': 'tts', 'tts': 'tts',
            'image': 'text2image', 'text2image': 'text2image',
            'image_editing': 'image_editing', 'image-editing': 'image_editing',
            'video': 'text2video', 'text2video': 'text2video',
            'multimodal_embedding': 'multimodal_embedding',
        }.get(declared, '')
    if outputs & {'embeddings', 'embedding'}:
        return 'multimodal_embedding' if inputs - {'text'} else 'embed'
    if 'rerank' in outputs:
        return 'rerank'
    if 'transcription' in outputs:
        return 'stt'
    if 'speech' in outputs:
        return 'tts'
    if 'video' in outputs:
        return 'text2video'
    if 'image' in outputs:
        # Image inputs can be optional references for an image-generation API;
        # they do not prove a separate image-editing adapter is available.
        return 'text2image'
    if outputs == {'audio'}:
        return 'tts'
    if outputs == {'text'}:
        return 'stt' if 'audio' in inputs and 'text' not in inputs else 'llm'
    return ''


def from_models_dev(entry, fallback_name):
    modalities = entry.get('modalities') or {}
    limit = entry.get('limit') or {}
    name = str(entry.get('id') or fallback_name).strip()
    model = blank_model(name)
    model['input'] = normalize_modalities(modalities.get('input'))
    model['output'] = normalize_modalities(modalities.get('output'))
    model['context'] = int(limit.get('context') or 0)
    model['max_input'] = int(limit.get('input') or 0)
    model['max_output'] = int(limit.get('output') or 0)
    model['model_type'] = model_type(entry.get('type'), model)
    return model


def from_openrouter(entry):
    architecture = entry.get('architecture') or {}
    name = str(entry.get('id') or '').strip()
    model = blank_model(name)
    model['input'] = normalize_modalities(architecture.get('input_modalities'))
    model['output'] = normalize_modalities(architecture.get('output_modalities'))
    model['context'] = int(entry.get('context_length') or 0)
    top = entry.get('top_provider') or {}
    model['max_output'] = int(top.get('max_completion_tokens') or 0)
    # Live task modalities take precedence over optional declared metadata.
    model['model_type'] = model_type(None if model['output'] else entry.get('model_type') or entry.get('type'), model)
    return model


def finalize(model):
    for field in ['context', 'max_input', 'max_output']:
        model[field] = max(0, model[field])
    if model['max_input'] > model['context'] > 0:
        model['max_input'] = model['context']
    return model


def collect_models_dev(catalog, keys):
    merged = {}
    for key in keys:
        provider = catalog.get(key) or {}
        for fallback_name, entry in (provider.get('models') or {}).items():
            if not isinstance(entry, dict):
                continue
            model = from_models_dev(entry, fallback_name)
            if not model['name']:
                continue
            merged[model['name']] = model
        # Regional endpoints have different availability and credentials. Later
        # provider IDs are fallbacks, not additional models for the same URL.
        if merged:
            break
    return merged


def collect_recognition_models(models_dev, openrouter_models):
    models = list(openrouter_models.values())
    for provider_name, provider in models_dev.items():
        if not isinstance(provider, dict):
            continue
        for fallback_name, entry in (provider.get('models') or {}).items():
            if not isinstance(entry, dict):
                continue
            model = from_models_dev(entry, fallback_name)
            if not model['name']:
                continue
            # This supplier's models.dev entries mirror OpenRouter. Keep the
            # live task evidence instead of introducing a stale mirror conflict.
            if provider_name == 'openrouter' and model['name'] in openrouter_models:
                continue
            models.append(model)
    distinct = {}
    for model in models:
        # A name alone is not an identity: different suppliers may use the same
        # ID for different tasks. Consumers must see those conflicting records.
        identity = (model['name'], model['model_type'], tuple(model['input']), tuple(model['output']))
        if identity not in distinct:
            distinct[identity] = finalize(model)
    return [distinct[identity] for identity in sorted(distinct)]


def keep_sensenova(name):
    lowered = name.lower()
    return lowered.startswith('sensenova') or lowered.startswith('sensechat') or lowered.startswith('sense-')


def output_paths(args):
    if args.backend_out or args.frontend_out:
        paths = []
        if args.backend_out:
            paths.append(args.backend_out)
        if args.frontend_out:
            paths.append(args.frontend_out)
        return paths
    paths = [os.path.join(
        os.path.dirname(__file__), '..', 'src', 'modules', 'modelProvider', 'upstreamModelCatalog.json',
    )]
    if os.path.isdir(os.path.join(ROOT, 'backend', 'core')):
        paths.append(os.path.join(ROOT, 'backend', 'core', 'config', 'upstream_model_catalog.json'))
    return paths


def utc_now():
    return datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')


def build_catalog(models_dev, openrouter):
    if not isinstance(models_dev, dict) or not models_dev:
        raise ValueError('models.dev returned an empty or invalid provider catalog')
    if not isinstance(openrouter, dict) or not isinstance(openrouter.get('data'), list) or not openrouter['data']:
        raise ValueError('OpenRouter returned an empty or invalid model catalog')
    providers = {}
    for provider_name, keys in PROVIDER_SOURCES.items():
        merged = collect_models_dev(models_dev, keys)
        if provider_name == 'SenseNova':
            merged = {name: model for name, model in merged.items() if keep_sensenova(name)}
        providers[provider_name] = [finalize(merged[name]) for name in sorted(merged)]

    if not any(providers.values()):
        raise ValueError('models.dev has no models for supported providers')
    metadata = collect_models_dev(models_dev, ['openrouter'])
    openrouter_models = {}
    for entry in openrouter.get('data') or []:
        if not isinstance(entry, dict):
            continue
        model = from_openrouter(entry)
        if not model['name']:
            continue
        # Live API decides availability, task modalities and limits. Only use
        # type enrichment when the live response has no output evidence at all.
        known = metadata.get(model['name'], {})
        if not model['output'] and not model['model_type']:
            model['model_type'] = known.get('model_type', '')
        openrouter_models[model['name']] = model
    providers['OpenRouter'] = [finalize(openrouter_models[name]) for name in sorted(openrouter_models)]

    if not openrouter_models:
        raise ValueError('OpenRouter has no valid model IDs')
    return {
        'schema_version': SCHEMA_VERSION,
        'generated_at': utc_now(),
        'sources': [MODELS_DEV_URL, OPENROUTER_URL],
        'providers': providers,
        'recognition_models': collect_recognition_models(models_dev, openrouter_models),
    }


def read_snapshot(path):
    try:
        with open(path, encoding='utf-8') as handle:
            payload = json.load(handle)
        version = payload.get('schema_version', 0)
        if version not in (0, 1, 2, SCHEMA_VERSION):
            return None
        payload['schema_version'] = version
        providers = payload['providers']
        if not isinstance(providers, dict) or not any(providers.values()):
            return None
        datetime.strptime(payload['generated_at'], '%Y-%m-%dT%H:%M:%SZ')
        model_lists = list(providers.values())
        if version == SCHEMA_VERSION:
            recognition = payload.get('recognition_models')
            if not isinstance(recognition, list) or not recognition:
                return None
            model_lists.append(recognition)
        for models in model_lists:
            if not isinstance(models, list):
                return None
            for model in models:
                # The first generator did not write these fields. Preserve its
                # data offline, but keep version 0 so the next build retries.
                if version == 0:
                    model.setdefault('max_input', 0)
                    model.setdefault('model_type', '')
                if not isinstance(model['name'], str) or not model['name'].strip():
                    return None
                if not isinstance(model['model_type'], str):
                    return None
                if any(
                    not isinstance(model[key], list) or any(not isinstance(v, str) or not v.strip() for v in model[key])
                    for key in ['input', 'output']
                ):
                    return None
                if any(
                    not isinstance(model[key], int) or model[key] < 0
                    for key in ['context', 'max_input', 'max_output']
                ):
                    return None
        return payload
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        return None


def write_snapshot(paths, payload):
    encoded = json.dumps(payload, ensure_ascii=False, indent=2) + '\n'
    for path in paths:
        directory = os.path.dirname(os.path.abspath(path))
        os.makedirs(directory, exist_ok=True)
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=directory, delete=False) as handle:
                temporary = handle.name
                handle.write(encoded)
            os.replace(temporary, path)
        finally:
            if temporary and os.path.exists(temporary):
                os.unlink(temporary)
        print(path, sum(len(items) for items in payload['providers'].values()))


def generate(paths, refresh=False):
    snapshots = [snapshot for path in paths if (snapshot := read_snapshot(path))]
    previous = max(snapshots, key=lambda item: item['generated_at']) if snapshots else None
    if previous and previous['schema_version'] == SCHEMA_VERSION and not refresh:
        generated = datetime.strptime(previous['generated_at'], '%Y-%m-%dT%H:%M:%SZ').replace(tzinfo=timezone.utc)
        if 0 <= (datetime.now(timezone.utc) - generated).total_seconds() < 86400:
            write_snapshot(paths, previous)
            return
    try:
        payload = build_catalog(fetch_json(MODELS_DEV_URL), fetch_json(OPENROUTER_URL))
    except (OSError, http.client.HTTPException, ValueError, TypeError, AttributeError) as error:
        if not previous:
            raise
        print(
            f'WARNING: catalog refresh failed ({error}); retaining snapshot from {previous["generated_at"]}',
            file=sys.stderr,
        )
        payload = previous
    write_snapshot(paths, payload)


def main():
    parser = argparse.ArgumentParser(
        description='Fetch the models.dev and OpenRouter catalog; reuse a valid snapshot for 24 hours.',
    )
    parser.add_argument('--backend-out')
    parser.add_argument('--frontend-out')
    parser.add_argument('--refresh', action='store_true', help='Refresh even if a recent snapshot is available.')
    args = parser.parse_args()
    generate(output_paths(args), refresh=args.refresh)
    return 0


if __name__ == '__main__':
    sys.exit(main())

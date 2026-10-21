#!/usr/bin/env python3
"""Build the bundled model catalog from models.dev and OpenRouter.

Cherry Studio generates provider metadata the same way: fetch both public
catalogs, union modalities, and keep the larger context window. This script
writes the compact JSON the backend seeds and the frontend renders.
"""

import json
import os
import sys
import urllib.request
from datetime import datetime, timezone

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', '..'))
OUTPUTS = [
    os.path.join(ROOT, 'backend', 'core', 'config', 'upstream_model_catalog.json'),
    os.path.join(ROOT, 'frontend', 'src', 'modules', 'modelProvider', 'upstreamModelCatalog.json'),
]

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

MODALITY_ORDER = ['text', 'image', 'audio', 'video', 'file']
MODALITY_MAP = {
    'text': 'text',
    'image': 'image',
    'audio': 'audio',
    'video': 'video',
    'pdf': 'file',
    'file': 'file',
}


def fetch_json(url):
    request = urllib.request.Request(url, headers={'Accept': 'application/json', 'User-Agent': 'LazyMind-catalog'})
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.load(response)


def normalize_modalities(values):
    found = []
    for value in values or []:
        mapped = MODALITY_MAP.get(str(value).strip().lower())
        if mapped and mapped not in found:
            found.append(mapped)
    return [item for item in MODALITY_ORDER if item in found]


def blank_model(name):
    return {'name': name, 'input': [], 'output': [], 'context': 0, 'max_output': 0}


def merge_model(current, incoming):
    current['input'] = normalize_modalities(list(current['input']) + list(incoming['input']))
    current['output'] = normalize_modalities(list(current['output']) + list(incoming['output']))
    current['context'] = max(int(current['context'] or 0), int(incoming['context'] or 0))
    current['max_output'] = max(int(current['max_output'] or 0), int(incoming['max_output'] or 0))
    if current['max_output'] > current['context'] > 0:
        current['max_output'] = 0
    return current


def from_models_dev(entry, fallback_name):
    modalities = entry.get('modalities') or {}
    limit = entry.get('limit') or {}
    name = str(entry.get('id') or fallback_name).strip()
    model = blank_model(name)
    model['input'] = normalize_modalities(modalities.get('input'))
    model['output'] = normalize_modalities(modalities.get('output'))
    model['context'] = int(limit.get('context') or 0)
    model['max_output'] = int(limit.get('output') or 0)
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
    return model


def finalize(model):
    if not model['input'] and not model['output']:
        model['input'] = ['text']
        model['output'] = ['text']
    if model['max_output'] > model['context'] > 0:
        model['max_output'] = 0
    if model['context'] <= 0:
        model['context'] = 0
    if model['max_output'] <= 0:
        model['max_output'] = 0
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
            merged[model['name']] = merge_model(merged.get(model['name'], blank_model(model['name'])), model)
    return merged


def keep_sensenova(name):
    lowered = name.lower()
    return lowered.startswith('sensenova') or lowered.startswith('sensechat') or lowered.startswith('sense-')


def main():
    models_dev = fetch_json('https://models.dev/api.json')
    openrouter = fetch_json('https://openrouter.ai/api/v1/models')
    providers = {}
    for provider_name, keys in PROVIDER_SOURCES.items():
        merged = collect_models_dev(models_dev, keys)
        if provider_name == 'SenseNova':
            merged = {name: model for name, model in merged.items() if keep_sensenova(name)}
        providers[provider_name] = [finalize(merged[name]) for name in sorted(merged)]

    openrouter_models = collect_models_dev(models_dev, ['openrouter'])
    for entry in openrouter.get('data') or []:
        if not isinstance(entry, dict):
            continue
        model = from_openrouter(entry)
        if not model['name']:
            continue
        openrouter_models[model['name']] = merge_model(
            openrouter_models.get(model['name'], blank_model(model['name'])),
            model,
        )
    providers['OpenRouter'] = [finalize(openrouter_models[name]) for name in sorted(openrouter_models)]

    payload = {
        'generated_at': datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'),
        'sources': ['https://models.dev/api.json', 'https://openrouter.ai/api/v1/models'],
        'providers': providers,
    }
    encoded = json.dumps(payload, ensure_ascii=False, indent=2) + '\n'
    for path in OUTPUTS:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'w', encoding='utf-8') as handle:
            handle.write(encoded)
        print(path, sum(len(items) for items in providers.values()))
    return 0


if __name__ == '__main__':
    sys.exit(main())

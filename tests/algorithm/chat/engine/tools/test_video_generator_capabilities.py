from __future__ import annotations

from unittest import mock

import lazyllm

from lazymind import model_config


def test_video_generator_identity_reads_request_dynamic_model_without_secrets():
    previous = lazyllm.globals['config'].get('dynamic_model_configs')
    try:
        lazyllm.globals['config']['dynamic_model_configs'] = {
            'video_generator': {
                'multimodal': {
                    'source': 'qwen',
                    'model': 'wan3.0-video',
                    'url': 'https://example.invalid',
                },
            },
        }
        with mock.patch.object(
            model_config,
            'load_model_config',
            return_value={
                'video_generator': {'source': 'dynamic', 'type': 'text2video'},
            },
        ):
            identity = model_config.get_model_role_runtime_identity(
                'video_generator', config_path='ignored',
            )
    finally:
        lazyllm.globals['config']['dynamic_model_configs'] = previous

    assert identity == {
        'role': 'video_generator',
        'source': 'qwen',
        'model': 'wan3.0-video',
    }

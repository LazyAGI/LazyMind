from __future__ import annotations

import inspect
from unittest import mock

import docstring_parser
import lazyllm

from lazymind.chat.engine.tools import multimodal
from lazymind.chat.service.component import tool_registry
from lazymind import model_config


def test_video_generator_schema_describes_supported_model_modes():
    description = docstring_parser.parse(
        inspect.getdoc(multimodal.video_generator),
    ).description

    assert 'wan3.0-video' in description
    assert 'wan2.6-t2v' in description
    assert 'Wan-AI/Wan2.2-I2V-A14B' in description
    assert 'Doubao Seedance' in description
    assert 'mutually exclusive' in description


def test_video_generator_prompt_identifies_request_selected_model():
    with mock.patch.object(
        tool_registry,
        'get_model_role_runtime_identity',
        return_value={
            'role': 'video_generator',
            'source': 'qwen',
            'model': 'wan3.0-video',
        },
    ):
        appendix = tool_registry._video_generator_prompt_appendix()

    assert 'Provider: `qwen`; model: `wan3.0-video`' in appendix['tool_policy']
    assert appendix['output_contract'] == (
        tool_registry.VIDEO_MARKDOWN_OUTPUT_APPENDIX['output_contract']
    )


def test_video_generator_passes_first_and_last_frame_semantics():
    with mock.patch.object(
        multimodal,
        '_resolve_source_image_paths',
        side_effect=lambda urls: [f'/resolved/{url}' for url in urls],
    ), mock.patch.object(
        multimodal,
        'run_video_model',
        return_value={'local_path': '/tmp/video.mp4'},
    ) as run:
        multimodal.video_generator(
            'wave once',
            first_frame_url='first.png',
            last_frame_url='last.png',
        )

    assert run.call_args.kwargs['files'] == [
        '/resolved/first.png',
        '/resolved/last.png',
    ]
    assert run.call_args.kwargs['image_semantics'] == ['first_frame', 'last_frame']
    assert run.call_args.kwargs['ratio'] == 'adaptive'


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

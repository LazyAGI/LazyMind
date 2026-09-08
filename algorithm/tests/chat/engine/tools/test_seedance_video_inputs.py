from __future__ import annotations

from pathlib import Path
import inspect
import sys
from unittest import mock
from types import SimpleNamespace

import docstring_parser

sys.path.insert(0, str(Path(__file__).resolve().parents[4]))

from lazyllm.module.llms.onlinemodule.supplier.doubao import DoubaoText2Video  # noqa: E402
from lazyllm.module.llms.onlinemodule.supplier import doubao as doubao_supplier  # noqa: E402
from lazymind.chat.engine.tools import multimodal  # noqa: E402
from lazymind.chat.engine.tools.infra import video_generation_support  # noqa: E402
from lazymind.chat.service.component import tool_registry  # noqa: E402


def test_video_generator_schema_describes_model_capability_matrix():
    parsed = docstring_parser.parse(inspect.getdoc(multimodal.video_generator))

    assert 'wan3.0-video' in parsed.description
    assert 'wan2.6-t2v' in parsed.description
    assert 'Wan-AI/Wan2.2-I2V-A14B' in parsed.description
    assert 'Doubao Seedance' in parsed.description
    assert 'mutually exclusive' in parsed.description


def test_video_generator_prompt_names_request_selected_model():
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
    assert appendix['output_contract'] == tool_registry.VIDEO_MARKDOWN_OUTPUT_APPENDIX['output_contract']


def test_video_generator_orders_seedance_first_and_last_frame_inputs():
    with mock.patch.object(
        multimodal,
        '_resolve_source_image_paths',
        side_effect=lambda urls: [f'/resolved/{url}' for url in urls],
    ), mock.patch.object(
        multimodal,
        'run_video_model',
        return_value={'local_path': '/tmp/video.mp4'},
    ) as run:
        result = multimodal.video_generator(
            'wave once',
            first_frame_url='first.png',
            last_frame_url='last.png',
        )

    assert result == {'local_path': '/tmp/video.mp4'}
    assert run.call_args.kwargs['files'] == [
        '/resolved/first.png',
        '/resolved/last.png',
    ]
    assert run.call_args.kwargs['image_semantics'] == [
        'first_frame',
        'last_frame',
    ]
    assert run.call_args.kwargs['ratio'] == 'adaptive'


def test_video_generator_keeps_multiple_seedance_references_in_reference_mode():
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
            'use all three characters as references',
            reference_urls=['ref-a.png', 'ref-b.png', 'ref-c.png'],
            ratio='1:1',
        )

    assert run.call_args.kwargs['files'] == [
        '/resolved/ref-a.png',
        '/resolved/ref-b.png',
        '/resolved/ref-c.png',
    ]
    assert run.call_args.kwargs['image_semantics'] == ['reference_image'] * 3
    assert run.call_args.kwargs['ratio'] == '1:1'


def test_video_generator_rejects_mixed_seedance_frame_and_reference_modes():
    with mock.patch.object(
        multimodal,
        '_resolve_source_image_paths',
        side_effect=AssertionError('must reject before resolving'),
    ):
        try:
            multimodal.video_generator(
                'invalid mixed mode',
                first_frame_url='first.png',
                reference_urls=['ref.png'],
            )
        except Exception as exc:  # noqa: BLE001
            assert 'cannot be combined' in str(exc)
        else:
            raise AssertionError('mixed Seedance modes must be rejected')


def test_video_runtime_passes_roles_only_to_role_aware_provider():
    provider = mock.Mock(return_value={'encoded': 'result'})
    provider.SUPPORTED_IMAGE_ROLES = True
    with mock.patch.object(
        video_generation_support,
        'AutoModel',
        return_value=provider,
    ), mock.patch.object(
        video_generation_support,
        '_parse_generated_files',
        return_value=['/tmp/provider-output.mp4'],
    ), mock.patch.object(
        video_generation_support,
        '_relocate_generated_video_to_upload',
        return_value='/tmp/uploaded.mp4',
    ), mock.patch.object(
        video_generation_support,
        '_register_generated_image_paths',
    ), mock.patch.object(
        video_generation_support,
        '_build_video_payload',
        return_value={'local_path': '/tmp/uploaded.mp4'},
    ):
        result = video_generation_support.run_video_model(
            'video_generator',
            'wave once',
            files=['/tmp/first.png', '/tmp/last.png'],
            image_semantics=['first_frame', 'last_frame'],
            ratio='adaptive',
        )

    assert provider.call_args.kwargs['image_roles'] == ['first_frame', 'last_frame']
    assert 'Image 1 is the required first frame' in provider.call_args.args[0]
    assert result['image_semantics'] == ['first_frame', 'last_frame']


def test_doubao_seedance_preserves_first_and_last_frame_roles():
    module = object.__new__(DoubaoText2Video)
    content = module._build_content(
        'Image 1 is the first frame and Image 2 is the last frame.',
        files=[
            'data:image/png;base64,Zmlyc3Q=',
            'https://cdn.example.com/last.png',
        ],
        image_roles=['first_frame', 'last_frame'],
        resolution='720p',
        duration=5,
        ratio='1:1',
    )

    assert content[0] == {
        'type': 'text',
        'text': 'Image 1 is the first frame and Image 2 is the last frame.',
    }
    assert content[1:] == [
        {
            'type': 'image_url',
            'image_url': {'url': 'data:image/png;base64,Zmlyc3Q='},
            'role': 'first_frame',
        },
        {
            'type': 'image_url',
            'image_url': {'url': 'https://cdn.example.com/last.png'},
            'role': 'last_frame',
        },
    ]


def test_doubao_seedance_submits_render_options_as_top_level_api_fields():
    module = object.__new__(DoubaoText2Video)
    tasks = SimpleNamespace(
        create=mock.Mock(return_value=SimpleNamespace(id='task-1')),
        get=mock.Mock(return_value=SimpleNamespace(
            status='succeeded',
            content=SimpleNamespace(video_url='https://cdn.example.com/out.mp4'),
        )),
    )
    client = SimpleNamespace(content_generation=SimpleNamespace(tasks=tasks))
    response = SimpleNamespace(content=b'video-bytes')

    with mock.patch.object(module, '_ark_client', return_value=client), mock.patch.object(
        doubao_supplier.requests,
        'get',
        return_value=response,
    ), mock.patch.object(
        doubao_supplier,
        'bytes_to_file',
        return_value=['/tmp/out.mp4'],
    ):
        module._forward(
            input='Use the supplied subject as a visual reference.',
            files=['https://cdn.example.com/reference.png'],
            image_roles=['reference_image'],
            resolution='720p',
            duration=5,
            ratio='1:1',
            watermark=False,
            model='doubao-seedance-2-5-260628',
        )

    request = tasks.create.call_args.kwargs
    assert request['model'] == 'doubao-seedance-2-5-260628'
    assert request['resolution'] == '720p'
    assert request['duration'] == 5
    assert request['ratio'] == '1:1'
    assert request['watermark'] is False
    assert 'camera_fixed' not in request
    assert request['content'][0] == {
        'type': 'text',
        'text': 'Use the supplied subject as a visual reference.',
    }
    assert request['content'][1]['role'] == 'reference_image'
    assert request['omni_reference_task_type'] == 'auto'
    assert 'doubao-seedance-2-5-260628' in DoubaoText2Video.MODEL_NAMES

import importlib.util
import json
import sys
import types
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from types import SimpleNamespace


def _stub_module(name, **attributes):
    module = types.ModuleType(name)
    module.__dict__.update(attributes)
    if name in {'lazyllm', 'lazyllm.tools', 'lazyllm.tools.writer'}:
        module.__path__ = []
    return module


def _load_writer_tools():
    model_names = (
        'InputResource MediaAssetLibrary ModifyPlan PatchResult PatchSet '
        'SectionInstruction SectionInstructionList TargetDocument VisualInstruction '
        'VisualPlan WriterBlock WriterDocument WritingTask'
    ).split()
    tool_names = (
        'WriterContextTools WriterDraftingTools WriterMultimodalTools WriterPlanningTools '
        'WriterQualityTools WriterResourceTools WriterRevisionTools'
    ).split()
    log = SimpleNamespace(warning=lambda *args, **kwargs: None, info=lambda *args, **kwargs: None)
    stubs = {
        'lazyllm': _stub_module(
            'lazyllm', LOG=log, AutoModel=lambda **kwargs: object(),
            ThreadPoolExecutor=ThreadPoolExecutor,
        ),
        'lazyllm.tools': _stub_module('lazyllm.tools'),
        'lazyllm.tools.writer': _stub_module('lazyllm.tools.writer'),
        'lazyllm.tools.writer.data_models': _stub_module(
            'lazyllm.tools.writer.data_models', **{name: object for name in model_names},
        ),
        'lazyllm.tools.writer.tools': _stub_module(
            'lazyllm.tools.writer.tools', **{name: object for name in tool_names},
        ),
        'lazyllm.tools.writer.numbering': _stub_module(
            'lazyllm.tools.writer.numbering', materialize_markdown=lambda value: value,
        ),
        'lazyllm.tools.writer.provider': _stub_module(
            'lazyllm.tools.writer.provider', match_writer_provider=lambda value: None,
        ),
        'lazyllm.tools.writer.utils': _stub_module(
            'lazyllm.tools.writer.utils',
            render_block_markdown=lambda value, **kwargs: str(value),
            render_document_markdown=lambda value: str(value),
            save_artifact_json=lambda *args, **kwargs: '',
            writer_document_to_markdown=lambda value: str(value),
        ),
    }
    previous = {name: sys.modules.get(name) for name in stubs}
    sys.modules.update(stubs)
    try:
        root = Path(__file__).resolve().parents[2]
        path = root / 'lazymind' / 'chat' / 'engine' / 'tools' / 'writer.py'
        spec = importlib.util.spec_from_file_location('writer_tools_for_recovery_test', path)
        assert spec and spec.loader
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        for name, module in previous.items():
            if module is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = module


def _instruction(title='研究方法'):
    return SimpleNamespace(section_title=title)


def test_markdown_section_uses_relative_heading_levels_and_ignores_fenced_code():
    writer = _load_writer_tools()

    result = writer._normalize_streamed_markdown_section(
        '## 研究方法\n\n### 研究方法\n\n#### 研究设计\n\n##### 数据来源\n\n'
        '```markdown\n# 示例标题\n```',
        _instruction(),
    )

    assert result.count('## 研究方法') == 1
    assert '### 研究设计' in result
    assert '#### 数据来源' in result
    assert '```markdown\n# 示例标题\n```' in result


def test_heading_validation_failure_is_recovered_and_checkpointed(monkeypatch, tmp_path):
    writer = _load_writer_tools()
    calls = []

    class FakeInstruction:
        @classmethod
        def model_validate(cls, value):
            return SimpleNamespace(section_title=value['section_title'])

    class FakeStream:
        result_called = False

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return None

        def __iter__(self):
            yield '## 研究方法\n\n'
            yield '### 研究方法\n\n'
            yield '#### 数据来源\n\n正文'
            # DraftPreviewStream finalizes while the iterator is being exhausted,
            # before callers can reach result(). Match that real lifecycle here.
            raise ValueError(writer._MARKDOWN_DRAFT_ROOT_ERROR)

        def result(self):
            FakeStream.result_called = True
            raise AssertionError('result() must not be reached after iterator finalization fails')

    class FakeDrafting:
        def __init__(self, **kwargs):
            pass

        def stream_draft_section(self, **kwargs):
            calls.append(kwargs)
            return FakeStream()

    monkeypatch.setattr(writer, 'SectionInstruction', FakeInstruction)
    monkeypatch.setattr(writer, 'WriterDraftingTools', FakeDrafting)
    monkeypatch.setattr(writer, '_temp_root', lambda: tmp_path / 'temporary')
    monkeypatch.setattr(writer, '_write_input_artifact', lambda *args, **kwargs: '/input.json')
    (tmp_path / 'temporary').mkdir()
    checkpoint_dir = tmp_path / 'checkpoints'
    instructions = json.dumps({'instructions': [{'section_title': '研究方法'}]})
    toolkit = writer.WriterToolkitBase()

    first = json.loads(toolkit.stream_draft_blocks_markdown(
        writing_task_json='{}',
        section_instructions_json=instructions,
        writing_context_json='{}',
        on_delta=lambda value: None,
        checkpoint_dir=str(checkpoint_dir),
    ))
    second = json.loads(toolkit.stream_draft_blocks_markdown(
        writing_task_json='{}',
        section_instructions_json=instructions,
        writing_context_json='{}',
        on_delta=lambda value: None,
        checkpoint_dir=str(checkpoint_dir),
    ))

    assert len(calls) == 1
    assert not FakeStream.result_called
    assert first == second
    assert first[0].startswith('## 研究方法')
    assert '### 数据来源' in first[0]

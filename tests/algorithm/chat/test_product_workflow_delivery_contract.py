import importlib.util
import tempfile
from pathlib import Path
from unittest import mock

import lazyllm
import yaml

from lazymind.chat.engine.subagent.context import SubAgentContext, set_context


ROOT = Path(__file__).resolve().parents[3]
WORKFLOW_ROOT = ROOT / 'workflows' / 'product_solution_delivery'


def _load_writer_bridge():
    path = WORKFLOW_ROOT / 'scripts' / 'writer_bridge.py'
    spec = importlib.util.spec_from_file_location('product_writer_bridge_contract_test', path)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_chapter_publisher_emits_stable_file_items():
    bridge = _load_writer_bridge()
    saved = []

    def fake_save_artifact(**kwargs):
        saved.append(kwargs)
        return {'status': 'ok'}

    with mock.patch.object(bridge, '_save_artifact', side_effect=fake_save_artifact):
        result = bridge._publish_chapter_artifacts(
            'review', ['/workspace/chapter-1.md', '/workspace/chapter-2.md'],
        )

    assert result == {
        'slot': 'review_chapters',
        'expected_count': 2,
        'published_count': 2,
        'complete': True,
        'warnings': [],
    }
    assert [item['key'] for item in saved] == ['review_chapters', 'review_chapters']
    assert [item['value'] for item in saved] == [
        '/workspace/chapter-1.md', '/workspace/chapter-2.md',
    ]
    assert [item['content_type'] for item in saved] == ['file', 'file']
    assert [item['publisher_list_index'] for item in saved] == [0, 1]
    assert all(item['internal_publish'] for item in saved)


def test_chapter_publisher_matches_real_subagent_artifact_contract():
    bridge = _load_writer_bridge()
    events = []

    class FakeDB:
        @staticmethod
        def next_artifact_seq(_task_id, _key):
            return 1

    previous = lazyllm.globals.get('subagent_ctx')
    with tempfile.TemporaryDirectory() as directory:
        workspace = Path(directory)
        chapters = []
        for index in range(2):
            chapter = workspace / f'chapter-{index + 1}.md'
            chapter.write_text(f'# chapter {index + 1}\n', encoding='utf-8')
            chapters.append(str(chapter))
        context = SubAgentContext(
            task_id='task-product-chapters',
            conversation_id='conversation-product',
            agent_type='workflow_step',
            objective='publish chapters',
            params={'output_slot_types': {'review_chapters': 'file'}},
            workspace_path=str(workspace),
            input_slots=[],
            output_slots=['review_chapters'],
            db=FakeDB(),
            emit=events.append,
        )
        set_context(context)
        try:
            result = bridge._publish_chapter_artifacts('review', chapters)
        finally:
            if previous is None:
                lazyllm.globals.pop('subagent_ctx', None)
            else:
                lazyllm.globals['subagent_ctx'] = previous

    assert result['complete'] is True
    assert [event['content_type'] for event in events] == ['file', 'file']
    assert [event['seq'] for event in events] == [1, 2]
    assert [event['value']['list_index'] for event in events] == [0, 1]
    assert all('path' in event['value'] for event in events)


def test_chapter_publish_failure_is_reported_but_does_not_abort_remaining_items():
    bridge = _load_writer_bridge()
    calls = []

    def fake_save_artifact(**kwargs):
        calls.append(kwargs['publisher_list_index'])
        if kwargs['publisher_list_index'] == 0:
            raise RuntimeError('temporary artifact sink failure')
        return {'status': 'ok'}

    with mock.patch.object(bridge, '_save_artifact', side_effect=fake_save_artifact):
        result = bridge._publish_chapter_artifacts(
            'prd', ['/workspace/chapter-1.md', '/workspace/chapter-2.md'],
        )

    assert calls == [0, 1]
    assert result['published_count'] == 1
    assert result['complete'] is False
    assert result['warnings'] == [
        'prd_chapters[1] publish failed: temporary artifact sink failure',
    ]


def test_document_bridge_keeps_required_paths_and_hides_raw_chapter_list():
    bridge = _load_writer_bridge()
    chapters = ['/workspace/chapter-1.md', '/workspace/chapter-2.md']
    publish_result = {
        'slot': 'design_chapters',
        'expected_count': 2,
        'published_count': 1,
        'complete': False,
        'warnings': ['one optional chapter was not published'],
    }

    with (
        mock.patch.object(bridge, '_runtime_stage', return_value='design'),
        mock.patch.object(bridge, '_stage_contract'),
        mock.patch.object(bridge, '_required_bound_file', side_effect=[
            '/workspace/task.json', '/workspace/outline.md', '/workspace/context.json',
        ]),
        mock.patch.object(bridge, 'product_writer_plan_sections', return_value={
            'section_instructions': '/workspace/plan.json',
            'warnings': ['planning warning'],
        }),
        mock.patch.object(bridge, 'product_writer_write_sections', return_value=chapters),
        mock.patch.object(
            bridge, 'product_writer_assemble_draft', return_value='/workspace/document.md',
        ),
        mock.patch.object(
            bridge, 'product_writer_update_context', return_value='/workspace/final-context.json',
        ),
        mock.patch.object(
            bridge, '_publish_chapter_artifacts', return_value=publish_result,
        ),
    ):
        result = bridge.product_writer_generate_document_from_inputs('design')

    assert result['section_plan'] == '/workspace/plan.json'
    assert result['document'] == '/workspace/document.md'
    assert result['writing_context'] == '/workspace/final-context.json'
    assert result['chapter_count'] == 2
    assert result['chapter_publish'] == publish_result
    assert result['warnings'] == [
        'planning warning', 'one optional chapter was not published',
    ]
    assert 'chapter_files' not in result


def test_product_document_tabs_use_non_composite_collapsed_chapter_lists():
    workflow = yaml.safe_load(
        (WORKFLOW_ROOT / 'workflow.yaml').read_text(encoding='utf-8'),
    )
    state = yaml.safe_load(
        (WORKFLOW_ROOT / 'scenario' / 'state.yml').read_text(encoding='utf-8'),
    )
    tabs = {tab['id']: tab for tab in workflow['ui']['tabs']}

    for stage in ('direction', 'design', 'prd', 'review', 'handoff'):
        chapter_slot = f'{stage}_chapters'
        document_tab = tabs[f'{stage}_document']
        assert document_tab['layout'] == 'list'
        assert 'composite_layout' not in document_tab
        assert [slot['id'] for slot in document_tab['slots']] == [
            f'{stage}_document', chapter_slot,
        ]
        assert workflow['ui']['slots'][chapter_slot]['collapsed'] is True

        prompt = ' '.join(
            state['steps'][f'write_{stage}_document']['prompt'].split()
        )
        assert f'directly to {chapter_slot}' in prompt
        assert 'do not save that slot again' in prompt
        assert 'Chapter publish warnings are non-blocking' in prompt
        assert 'do not retry generation or call another tool for them' in prompt
        assert 'chapter_files' not in prompt

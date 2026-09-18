import json

import pytest
from lazyllm.tools.writer.data_models.revision import GeneratedRevision
from lazyllm.tools.writer.tools import WriterRevisionTools

from lazymind.document_tools.actions import DocumentActionError, invoke_document_action
from lazymind.rewrite import selection

ACTION = 'builtin:document.rewrite_selection.v1'


def fake_model(prompt):
    payload = json.loads(prompt.split('\n')[-1])
    return json.dumps({'results': [{'id': item['id'], 'content': item['content'].replace('原文', '润色')}
                                   for item in payload['paragraphs']]}, ensure_ascii=False)


def preview(source, ranges, tmp_path):
    return invoke_document_action(ACTION, 'preview', {
        'type': 'markdown' if isinstance(source, str) else 'ir',
        'instruction': '润色', 'selection_ranges': ranges,
    }, artifact=source, artifact_store=str(tmp_path), slot='draft_document')


def test_markdown_returns_whole_paragraphs_and_commits_exact_preview(monkeypatch, tmp_path):
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: fake_model)
    source = '前缀原文后缀。\n\n保持中间段。\n\n第三段原文。'
    result = preview(source, [
        {'selected_text': '第三段原文'},
        {'selected_text': '前缀原文'},
    ], tmp_path)
    assert len(result['results']) == 2
    assert result['results'][0]['preview'] == {'old_text': '前缀原文后缀。', 'new_text': '前缀润色后缀。'}
    assert all(item['patch']['type'] == 'string_replace_set' for item in result['results'])
    assert result['artifact']['value'] == source.replace('原文', '润色')
    execute = {'commit_token': result['commit']['token']}
    committed = invoke_document_action(ACTION, 'execute', execute, artifact=source,
                                       artifact_store=str(tmp_path), slot='draft_document')
    assert committed['artifact'] == result['artifact']
    with pytest.raises(DocumentActionError) as conflict:
        invoke_document_action(ACTION, 'execute', execute, artifact=source + '外部修改',
                               artifact_store=str(tmp_path), slot='draft_document')
    assert conflict.value.status_code == 409


def test_markdown_repeated_text_requires_offset_and_only_changes_one_occurrence(monkeypatch, tmp_path):
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: fake_model)
    source = '原文。\n\n原文。'
    with pytest.raises(DocumentActionError) as error:
        preview(source, [{'selected_text': '原文'}], tmp_path)
    assert error.value.error_code == 'SELECTION_AMBIGUOUS'
    result = preview(source, [{'selected_text': '原文', 'start': 5, 'end': 7}], tmp_path)
    assert result['artifact']['value'] == '原文。\n\n润色。'
    assert result['results'][0]['target']['target_start'] == 5


def test_markdown_same_paragraph_is_deduplicated_and_keeps_focus(monkeypatch, tmp_path):
    calls = []

    def generate(prompt):
        calls.append(json.loads(prompt.split('\n')[-1]))
        return fake_model(prompt)
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: generate)
    result = preview('前缀**原文**后缀。', [
        {'selected_text': '前缀原文'},
        {'selected_text': '后缀'},
    ], tmp_path)
    assert len(result['results']) == 1
    assert len(calls) == 1
    assert calls[0]['paragraphs'][0]['selected_quotes'] == ['前缀原文', '后缀']
    assert result['artifact']['value'] == '前缀**润色**后缀。'


def test_ir_reuses_whole_node_patch_pipeline_and_visible_tree(monkeypatch, tmp_path):
    source = {'document_id': 'doc', 'title': '只读标题', 'blocks': [
        {'node_id': 'heading', 'type': 'heading', 'content': '只读章节', 'children': [
            {'node_id': 'p1', 'type': 'paragraph', 'content': '前缀原文后缀',
             'spans': [{'text': '前缀', 'style': {'bold': True}}, {'text': '原文'}, {'text': '后缀'}],
             'provider_binding': {'block_id': 'external'}, 'provider_payload': {'retain': True}},
            {'node_id': 'p2', 'type': 'paragraph', 'content': '只读中段'},
            {'node_id': 'p3', 'type': 'paragraph', 'content': '原文。'},
        ]},
    ]}
    calls = []

    def generate(self, prompt, model):
        calls.append(prompt)
        assert model is GeneratedRevision
        assert '只读标题' in prompt and '只读章节' in prompt and '只读中段' in prompt
        assert 'NOT a strict modification boundary' in prompt
        # Changes outside the quote but inside its paragraph are intentionally valid.
        return model.model_validate({'changes': {
            'rewrite-selection-0': [{'content': '调整前缀润色后缀', 'spans': [
                {'text': '调整前缀', 'style': {'bold': True}}, {'text': '润色后缀'}]}],
            'rewrite-selection-1': [{'content': '润色。'}],
        }})
    monkeypatch.setattr(WriterRevisionTools, '_call_llm_structured', generate)
    monkeypatch.setattr('lazymind.document_tools.selection.AutoModel', lambda **_: object())
    result = preview(source, [
        {'node_id': 'p3', 'selected_text': '原文'},
        {'node_id': 'p1', 'selected_text': '原文'},
        {'node_id': 'p1', 'selected_text': '后缀'},
    ], tmp_path)
    assert len(calls) == 1
    assert [r['target']['node_id'] for r in result['results']] == ['p1', 'p3']
    assert result['results'][0]['preview']['old_text'] == '前缀原文后缀'
    assert result['results'][0]['preview']['new_text'] == '调整前缀润色后缀'
    assert all(r['patch']['type'] == 'writer_ir_patch' for r in result['results'])
    revised = result['artifact']['value']['blocks'][0]['children']
    assert revised[0]['provider_binding'] == {'block_id': 'external'}
    assert revised[0]['provider_payload'] == {'retain': True}
    assert revised[1]['content'] == '只读中段'


@pytest.mark.parametrize('arguments', [
    {'selection': {'type': 'markdown', 'selected_text': '原文'}},
    {'type': 'markdown', 'selection_ranges': []},
    {'type': 'markdown', 'selection_ranges': [{'type': 'markdown', 'selected_text': '原文'}]},
    {'type': 'ir', 'selection_ranges': [{'selected_text': '原文'}]},
    {'type': 'ir', 'selection_ranges': [{'node_id': 'p'}]},
])
def test_invalid_selection_contract_is_rejected(arguments, tmp_path):
    with pytest.raises(DocumentActionError) as error:
        invoke_document_action(ACTION, 'preview', {'instruction': '润色', **arguments},
                               artifact='原文。', artifact_store=str(tmp_path), slot='draft_document')
    assert error.value.status_code == 422


def test_more_than_256_targets_are_supported(monkeypatch, tmp_path):
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: fake_model)
    paragraphs = [f'第{i}段原文。' for i in range(257)]
    result = preview('\n\n'.join(paragraphs), [{'selected_text': p} for p in paragraphs], tmp_path)
    assert len(result['results']) == 257
    assert result['artifact']['value'] == '\n\n'.join(p.replace('原文', '润色') for p in paragraphs)


def test_ir_unchanged_paragraph_still_has_a_result(monkeypatch, tmp_path):
    monkeypatch.setattr('lazymind.document_tools.selection.AutoModel', lambda **_: object())
    monkeypatch.setattr(WriterRevisionTools, '_call_llm_structured', lambda self, prompt, model: model.model_validate({
        'changes': {'rewrite-selection-0': [{'content': '原文。'}]},
    }))
    result = preview({'document_id': 'doc', 'blocks': [
        {'node_id': 'p', 'type': 'paragraph', 'content': '原文。'},
    ]}, [{'node_id': 'p'}], tmp_path)
    assert len(result['results']) == 1
    assert result['results'][0]['preview'] == {'old_text': '原文。', 'new_text': '原文。'}
    assert result['results'][0]['patch']['payload']['hunks'] == []


def test_bad_markdown_model_response_returns_upstream_failure(monkeypatch, tmp_path):
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: lambda prompt: '{"results":[]}')
    with pytest.raises(DocumentActionError) as error:
        preview('原文。', [{'selected_text': '原文'}], tmp_path)
    assert error.value.status_code == 502


def test_markdown_heading_and_nested_list_preview_commit_preserve_markers(monkeypatch, tmp_path):
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: fake_model)
    source = '## 标题原文\n\n3. 保持父项\n   - 列表原文\n4. 不改'
    result = preview(source, [{'selected_text': '标题原文'}, {'selected_text': '列表原文'}], tmp_path)
    assert [item['target']['block_type'] for item in result['results']] == ['heading', 'list_item']
    assert result['artifact']['value'] == source.replace('原文', '润色')
    committed = invoke_document_action(ACTION, 'execute', {'commit_token': result['commit']['token']},
                                      artifact=source, artifact_store=str(tmp_path), slot='draft_document')
    assert committed['artifact'] == result['artifact']


def test_ir_heading_and_list_polish_keep_types_numbering_and_children(monkeypatch, tmp_path):
    monkeypatch.setattr('lazymind.document_tools.selection.AutoModel', lambda **_: object())
    monkeypatch.setattr(WriterRevisionTools, '_call_llm_structured', lambda self, prompt, model: model.model_validate({
        'changes': {'rewrite-selection-0': [{'content': '标题润色'}],
                    'rewrite-selection-1': [{'content': '列表润色'}]},
    }))
    source = {'document_id': 'doc', 'blocks': [
        {'node_id': 'h', 'type': 'heading', 'content': '标题原文', 'level': 2},
        {'node_id': 'li', 'type': 'list_item', 'content': '列表原文', 'numbering': {'ordered': True, 'start': 3},
         'children': [{'node_id': 'child', 'type': 'list_item', 'content': '保持子项'}]},
    ]}
    result = preview(source, [{'node_id': 'h'}, {'node_id': 'li'}], tmp_path)
    blocks = result['artifact']['value']['blocks']
    assert blocks[0]['type'] == 'heading' and blocks[0]['content'] == '标题润色'
    assert blocks[1]['type'] == 'list_item' and blocks[1]['content'] == '列表润色'
    assert blocks[1]['numbering'] == source['blocks'][1]['numbering']
    assert blocks[1]['children'][0]['content'] == '保持子项'

import hashlib
import json

import pytest

from lazymind.document_tools.actions import invoke_document_action, DocumentActionError
from lazymind.document_tools.artifacts import inspect_document
from lazymind.rewrite import selection


@pytest.mark.parametrize('old,new', [
    ('See [[Note A|label]].', 'See [[Note B|label]].'),
    ('Value $x+1$ here.', 'Value $x+2$ here.'),
    ('$$x+1$$', '$$x+2$$'),
    ('Value \\(x+1\\) here.', 'Value \\(x+2\\) here.'),
    ('Text[^a] here.', 'Text[^b] here.'),
    ('Text ^[a note] here.', 'Text ^[changed] here.'),
    ('Text here ^block-a', 'Text here ^block-b'),
    ('Text %%private%% here.', 'Text %%changed%% here.'),
])
def test_rewrite_rejects_changes_to_source_semantics(old, new, monkeypatch, tmp_path):
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: lambda prompt: json.dumps({
        'results': [{'id': '0', 'content': new}],
    }))
    with pytest.raises(DocumentActionError):
        invoke_document_action('builtin:document.rewrite_selection.v1', 'preview', {
            'type': 'markdown', 'instruction': 'Polish prose',
            'selection_ranges': [{'selected_text': old, 'start': 0, 'end': len(old)}],
        }, artifact=old, artifact_store=str(tmp_path), slot='draft_document')
    assert not list(tmp_path.rglob('*.md'))


def test_rewrite_allows_prose_changes_around_unchanged_source_semantics():
    old = 'Old [[Note|alias]] $x+1$ [^a] ^[nested [text]] %%comment%% ^block-a'
    new = old.replace('Old', 'New')
    selection.validate_paragraph_replacement(old, new)


def test_ir_rewrite_protects_source_syntax_too(monkeypatch, tmp_path):
    from lazyllm.tools.writer.tools import WriterRevisionTools
    monkeypatch.setattr('lazymind.document_tools.selection.AutoModel', lambda **_: object())
    monkeypatch.setattr(WriterRevisionTools, '_call_llm_structured', lambda self, prompt, model: model.model_validate({
        'changes': {'rewrite-selection-0': [{'content': 'See [[Other]] $x+2$'}]},
    }))
    source = {'document_id': 'fixture', 'blocks': [{'node_id': 'p', 'type': 'paragraph', 'content': 'See [[Note]] $x+1$'}]}
    with pytest.raises(DocumentActionError):
        invoke_document_action('builtin:document.rewrite_selection.v1', 'preview', {
            'type': 'ir', 'instruction': 'Polish prose', 'selection_ranges': [{'node_id': 'p'}],
        }, artifact=source, artifact_store=str(tmp_path), slot='draft_document')


def inspected(source, **hints):
    return inspect_document({'data': source, 'meta': {'writer_source': hints}}, 'text/markdown')


def test_inspection_projects_duplicate_fences_by_occurrence_without_private_metadata():
    fence = '```text\nA --> B\n```\n'
    source = '😀Intro\n\n' + fence + '\n' + fence
    hint = {'display': fence, 'language': 'mermaid'}
    result = inspected(source, code_fences=[hint, hint])
    context = result['render_context']
    assert context['source_hash'] == hashlib.sha256(source.encode()).hexdigest()
    assert context['code_fences'] == [
        {'start': source.index(fence), 'end': source.index(fence) + len(fence), 'language': 'mermaid'},
        {'start': source.rindex(fence), 'end': len(source), 'language': 'mermaid'},
    ]
    assert result['representation'] == 'markdown'
    assert 'writer_source' not in json.dumps(result)


def test_inspection_projects_image_dimensions_per_occurrence():
    source = '![image](image.png)\n\n![image](image.png)\n\n![300x200](image.png)'
    context = inspected(source, images={'image.png': {'raw_variants': [
        '![[image.png|100]]', '![[image.png|300]]', '![[image.png|300x200]]',
    ]}})['render_context']
    assert [(item['width'], item.get('height')) for item in context['images']] == [(100, None), (300, None), (300, 200)]
    assert [source[item['start']:item['end']] for item in context['images']] == [
        '![image](image.png)', '![image](image.png)', '![300x200](image.png)',
    ]


def test_missing_or_ambiguous_metadata_never_guesses_display_semantics():
    assert 'render_context' not in inspect_document('Plain', 'text/markdown')
    fence = '```text\nsame\n```\n'
    result = inspected(fence, code_fences=[{'display': fence, 'language': 'mermaid'}, {'display': fence, 'language': 'math'}])
    assert not result.get('render_context', {}).get('code_fences')
    source = '![image](image.png)'
    result = inspected(source, images={'image.png': {'raw_variants': ['![[image.png|100]]','![[image.png|300]]']}})
    assert not result.get('render_context', {}).get('images')


def test_source_projection_does_not_apply_image_dimensions_to_code_examples():
    source = '```markdown\n![image](image.png)\n```\n\n![image](image.png)'
    context = inspected(source, images={'image.png': {'raw_variants': ['![[image.png|300]]']}})['render_context']
    assert context['images'] == [{'start': source.rindex('!['), 'end': len(source), 'width': 300}]


def test_real_import_dimensions_survive_ordinary_save_and_reinspection(tmp_path):
    from lazyllm.tools.fs.supplier.obsidian import ObsidianFS, ObsidianNote
    from lazyllm.tools.writer.provider.obsidian import ObsidianWriterProvider
    from lazymind.document_tools.artifacts import save_document
    root = tmp_path.resolve()
    (root / '.obsidian').mkdir()
    (root / 'note.md').write_text('')
    (root / 'image.png').write_bytes(b'fixture')
    fs = ObsidianFS(token=str(root))
    note = ObsidianNote(vault=fs.discover_vaults()[0], relative_path='note.md', path=root / 'note.md')
    source, bridge = ObsidianWriterProvider()._to_writer_markdown('Old\n\n![[image.png|300x200]]', note, fs)
    saved = save_document(source.replace('Old', 'New'), source)['source_document']
    context = inspected(saved, images=bridge['images'])['render_context']
    assert [(image['width'], image['height']) for image in context['images']] == [(300, 200)]
    assert saved[context['images'][0]['start']:context['images'][0]['end']] == '![300x200](image.png)'


def test_source_projection_has_bounded_optional_processing():
    source = '```text\nx\n```\n' * 1001
    result = inspected(source, code_fences=[{'display': '```text\nx\n```\n', 'language': 'mermaid'}])
    assert 'render_context' not in result


@pytest.mark.parametrize('style', [{'math_source': 'x+1'}, {'notion:rich_text_type': 'equation', 'notion:equation': {'expression': 'x+1'}}])
@pytest.mark.parametrize('change', ['text', 'metadata', 'remove', 'prose'])
def test_ir_formula_spans_keep_formula_and_native_payload(style, change, monkeypatch, tmp_path):
    from copy import deepcopy
    from lazyllm.tools.writer.tools import WriterRevisionTools
    spans = [{'text': 'Old '}, {'text': 'x+1', 'style': style}, {'text': ' here.'}]
    revised = deepcopy(spans)
    if change == 'text':
        revised[1]['text'] = 'x+2'
    elif change == 'metadata':
        revised[1]['style'] = {'math_source': 'other'} if 'math_source' in style else {'notion:rich_text_type': 'equation', 'notion:equation': {'expression': 'x+2'}}
    elif change == 'remove':
        revised[1]['style'] = {}
    else:
        revised[0]['text'] = 'New '
    source = {'document_id': 'fixture', 'blocks': [{'node_id': 'p', 'type': 'paragraph', 'content': 'Old x+1 here.', 'spans': spans}]}
    replacement = {'content': ''.join(span['text'] for span in revised), 'spans': revised}
    monkeypatch.setattr('lazymind.document_tools.selection.AutoModel', lambda **_: object())
    monkeypatch.setattr(WriterRevisionTools, '_call_llm_structured', lambda self, prompt, model: model.model_validate({'changes': {'rewrite-selection-0': [replacement]}}))
    def preview():
        return invoke_document_action('builtin:document.rewrite_selection.v1', 'preview', {'type': 'ir', 'instruction': 'Polish', 'selection_ranges': [{'node_id': 'p'}]}, artifact=source, artifact_store=str(tmp_path), slot='draft_document')
    if change == 'prose':
        assert preview()['artifact']['value']['blocks'][0]['content'] == 'New x+1 here.'
    else:
        with pytest.raises(DocumentActionError):
            preview()


@pytest.mark.parametrize('change_hidden', [True, False])
def test_cross_paragraph_comments_use_full_document_context(change_hidden, monkeypatch, tmp_path):
    source = 'Before %%hidden\n\nsecret%% after'
    replacement = 'CHANGED%% after' if change_hidden else 'secret%% updated'
    generate = lambda prompt: json.dumps({'results': [{'id': '0', 'content': replacement}]})
    monkeypatch.setattr(selection, 'AutoModel', lambda **_: generate)
    start = source.index('secret')
    def action():
        return invoke_document_action('builtin:document.rewrite_selection.v1', 'preview', {'type': 'markdown', 'instruction': 'Polish', 'selection_ranges': [{'selected_text': 'secret', 'start': start, 'end': start + 6}]}, artifact=source, artifact_store=str(tmp_path), slot='draft_document')
    if change_hidden:
        with pytest.raises(DocumentActionError):
            action()
        with pytest.raises(selection.UnprocessableContentError):
            selection.rewrite_ranges(source, [{'start': start, 'end': start + 6, 'content': 'secret'}], 'Polish', generate=generate)
    else:
        assert action()['artifact']['value'] == source.replace('after', 'updated')


def test_crlf_fences_keep_the_provider_language_after_following_prose():
    from lazyllm.tools.writer.provider.github import _normalize_code_fences
    source, entries = _normalize_code_fences('```mermaid\r\nA --> B\r\n```\r\n\r\nAfter\r\n')
    context = inspected(source, code_fences=entries)['render_context']
    assert context['code_fences'] == [{'start': 0, 'end': source.index('\r\nAfter'), 'language': 'mermaid'}]


def test_real_provider_image_examples_do_not_hide_body_dimensions(tmp_path):
    from lazyllm.tools.fs.supplier.obsidian import ObsidianFS, ObsidianNote
    from lazyllm.tools.writer.provider.obsidian import ObsidianWriterProvider
    root = tmp_path.resolve()
    (root / '.obsidian').mkdir()
    (root / 'note.md').write_text('')
    (root / 'image.png').write_bytes(b'fixture')
    fs = ObsidianFS(token=str(root))
    note = ObsidianNote(vault=fs.discover_vaults()[0], relative_path='note.md', path=root / 'note.md')
    original = '```markdown\n![[image.png|100]]\n```\n\n`![[image.png|200]]`\n\n![[image.png|300x200]]'
    source, bridge = ObsidianWriterProvider()._to_writer_markdown(original, note, fs)
    assert len(bridge['images']['image.png']['raw_variants']) == 3
    context = inspected(source, images=bridge['images'])['render_context']
    assert context['images'] == [{'start': source.rindex('!['), 'end': len(source), 'width': 300, 'height': 200}]

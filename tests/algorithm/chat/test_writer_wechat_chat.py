from pathlib import Path
from io import BytesIO

import json
import pytest
import requests

from lazyllm.tools.writer.data_models import (
    PatchHunk,
    PatchSet,
    TargetDocument,
    WriterBlock,
    WriterDocument,
)
from lazyllm.tools.writer.provider.wechat import WeChatWriterProvider, wechat_placeholder_cover_png
from lazymind.document_tools import WriterResourceToolkit
from lazymind.document_tools.resources import convert_document, write_document
from lazyllm.tools.writer.provider.wechat import prepare_wechat_cover as _prepare_wechat_cover
from PIL import Image


def test_wechat_prompt_title_load_patch_and_write_back(monkeypatch):
    calls = []

    class Client:
        def __init__(self, token):
            assert token == 'stable-token'
            self.warnings = []

        def batch_get_drafts(self, offset, count, *, no_content=False):
            calls.append(('list', offset, count, no_content))
            return {
                'total_count': 2,
                'item': [
                    {'media_id': 'first', 'content': {'news_item': [
                        {'title': '另一篇文章'},
                        {'title': '需要修改的完整标题'},
                    ]}},
                    {'media_id': 'second', 'content': {'news_item': [
                        {'title': '需要修改的完整标题'},
                    ]}},
                ],
            }

        def get_draft(self, media_id):
            calls.append(('get', media_id))
            return {
                'update_time': 7,
                'news_item': [
                    {
                        'title': '另一篇文章',
                        'content': '<p>不能修改</p>',
                        'thumb_media_id': 'cover-0',
                    },
                    {
                        'title': '需要修改的完整标题',
                        'content': '<p style="color:red">需要修改</p>'
                        '<section data-card="keep"><custom-card /></section>',
                        'thumb_media_id': 'cover-1',
                    },
                ],
            }

        def update_draft(self, media_id, article, *, index=0):
            calls.append(('update', media_id, index, article))

    monkeypatch.setattr(
        'lazyllm.tools.writer.provider.wechat.WeChatClient', Client,
    )
    monkeypatch.setattr(
        WeChatWriterProvider,
        '_access_token',
        staticmethod(lambda: 'stable-token'),
    )

    toolkit = WriterResourceToolkit()
    loaded = json.loads(toolkit.load_document(
        '请把微信公众号草稿箱文章《需要修改的完整标题》中的“需要修改”改成“已经修改”',
    ))
    source = WriterDocument.model_validate(loaded['source_document'])
    changed = source.blocks[0].model_copy(deep=True)
    changed.content = '已经修改'
    changed.spans = []
    patch = PatchSet(
        patch_id='patch-title-match',
        target_doc_id=source.document_id,
        hunks=[PatchHunk(
            hunk_id='replace-paragraph',
            target_node_id=changed.node_id,
            modify_type='update',
            block=changed,
        )],
    )
    published = json.loads(toolkit.publish_revision(
        source_document_json=json.dumps(loaded['source_document'], ensure_ascii=False),
        patch_set_json=json.dumps(patch.model_dump(), ensure_ascii=False),
    ))

    assert calls[0] == ('list', 0, 20, True)
    assert calls[1] == ('get', 'first')
    assert calls[2] == ('get', 'first')
    update = calls[3]
    assert update[0:3] == ('update', 'first', 1)
    assert '<p>已经修改</p>' in update[3]['content']
    assert '<section data-card="keep"><custom-card /></section>' in update[3]['content']
    assert published['draft_document']['provider_binding']['document_id'] == 'first'


def test_wechat_prompt_title_not_found(monkeypatch):
    monkeypatch.setattr(
        WeChatWriterProvider,
        'list_drafts',
        lambda self: [{
            'media_id': 'draft-1',
            'content': {'news_item': [{'title': '真实完整标题'}]},
        }],
    )

    with pytest.raises(Exception, match='请输入草稿文章的准确完整标题'):
        WriterResourceToolkit().load_document(
            '请修改微信公众号草稿箱文章《不准确的标题》',
        )


@pytest.mark.parametrize(
    ('prompt', 'expected'),
    [
        ('请修改微信公众号草稿箱中的《目标文章》', True),
        ('请优化微信公众号草稿箱中的《目标文章》', True),
        ('请修改公众号草稿箱中的《目标文章》', False),
        ('请修改微信草稿箱中的《目标文章》', False),
        ('请查看微信公众号草稿箱中的《目标文章》', False),
    ],
)
def test_wechat_draft_revision_requires_all_explicit_terms(prompt, expected):
    assert WeChatWriterProvider.matches(prompt) is expected


def _document() -> WriterDocument:
    return WriterDocument(
        document_id='writer-cover',
        title='城市更新',
        stage='final',
        blocks=[WriterBlock(node_id='body', type='paragraph', content='正文内容')],
    )


@pytest.mark.parametrize('cover_source', ['article', 'generated', 'failed', 'unconfigured'])
def test_new_wechat_draft_prepares_and_uploads_cover(monkeypatch, tmp_path: Path, cover_source):
    with_images = cover_source == 'article'
    generated = tmp_path / 'generated.png'
    Image.new('RGB', (1200, 600), 'blue').save(generated)
    calls = {}

    def generate(prompt, **kwargs):
        calls['prompt'] = prompt
        calls['kwargs'] = kwargs
        if cover_source == 'failed':
            raise RuntimeError('model failed')
        return {'local_path': str(generated)}

    def request(method, url, **kwargs):
        assert method == 'POST'
        if url.endswith('/material/add_material'):
            calls['cover'] = kwargs['files']['media'][1]
            payload = {'media_id': 'cover-media'}
        elif url.endswith('/media/uploadimg'):
            payload = {'url': 'https://mmbiz.qpic.cn/body.png'}
        else:
            assert url.endswith('/draft/add')
            calls['article'] = json.loads(kwargs['data'])['articles'][0]
            payload = {'media_id': 'draft-media'}
        response = requests.Response()
        response.status_code = 200
        response._content = json.dumps(payload).encode()
        return response

    monkeypatch.setattr('lazyllm.tools.writer.provider.wechat.requests.request', request)
    monkeypatch.setattr(WeChatWriterProvider, '_access_token', staticmethod(lambda: 'stable-token'))
    monkeypatch.setattr('lazymind.chat.engine.tools.multimodal.image_generator', generate)
    monkeypatch.setattr('lazymind.model_config.is_model_role_available',
                        lambda role: cover_source not in {'article', 'unconfigured'})
    monkeypatch.setattr('lazymind.document_tools.resources._temp_root', lambda: tmp_path)
    document, media = _document(), None
    if with_images:
        media = {'library_id': 'images', 'assets': {}}
        for asset_id, color in [('second', 'green'), ('first', 'red')]:
            path = tmp_path / f'{asset_id}.png'
            Image.new('RGB', (1024, 1024), color).save(path)
            media['assets'][asset_id] = {'media_asset_id': asset_id, 'asset_type': 'image',
                                         'source_type': 'web_search', 'local_path': str(path)}
        document.blocks.extend(WriterBlock(node_id=key, type='image', references=[
            {'type': 'media_asset', 'id': key},
        ]) for key in ('first', 'second'))
    converted = convert_document(document, 'wechat', media_assets=media)
    result = write_document(converted, media_assets=media)

    assert result['success'] and calls['article']['thumb_media_id'] == 'cover-media'
    if cover_source in {'article', 'unconfigured'}:
        assert 'prompt' not in calls
    else:
        assert '城市更新' in calls['prompt']
        assert calls['kwargs'] == {'image_size': '1024x1024', 'batch_size': 1}
    with Image.open(BytesIO(calls['cover'])) as cover:
        assert cover.size == (900, 383)
        assert cover.format == 'PNG'
        if cover_source in {'article', 'generated'}:
            assert cover.getpixel((450, 191)) == ((255, 0, 0) if with_images else (0, 0, 255))
        else:
            assert calls['cover'] == wechat_placeholder_cover_png()


def test_wechat_cover_generation_does_not_run_for_existing_or_other_targets(
    tmp_path: Path,
):
    def generator(*args, **kwargs):
        raise AssertionError('must not run')

    existing = TargetDocument(adapter='wechat', doc_id='draft-1')
    notion = TargetDocument(adapter='notion')

    assert _prepare_wechat_cover(
        existing, _document(), tmp_path, generator=generator,
    ) is existing
    assert _prepare_wechat_cover(
        notion, _document(), tmp_path, generator=generator,
    ) is notion


def test_missing_provider_url_reports_writeback_error():
    from lazymind.document_tools.resources import _published_link
    from lazyllm.tools.agent import ToolExecutionError

    with pytest.raises(ToolExecutionError, match='no browser URL was returned'):
        _published_link(TargetDocument(adapter='wechat', doc_id='existing-doc'))

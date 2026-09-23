import pytest

from lazymind.document_tools import DocumentActionError, invoke_document_action
from lazyllm.tools.writer.provider.wechat import WeChatWriterProvider


def test_resolves_exact_draft_article_and_refreshes_expiring_url(monkeypatch):
    calls = []

    class Client:
        def __init__(self, token):
            assert token == 'fixture-token'

        def get_draft(self, media_id):
            calls.append(media_id)
            return {'news_item': [
                {'url': 'https://mp.weixin.qq.com/s/wrong-article'},
                {'url': f'https://mp.weixin.qq.com/s?tempkey=fixture-{len(calls)}'},
            ]}

    monkeypatch.setattr('lazyllm.tools.writer.provider.wechat.WeChatClient', Client)
    monkeypatch.setattr(WeChatWriterProvider, '_access_token', staticmethod(lambda: 'fixture-token'))
    for number in (1, 2):
        result = invoke_document_action('builtin:document.wechat_draft_url.v1', 'preview',
                                        {'media_id': 'selected-draft', 'article_index': 1})
        assert result == {'url': f'https://mp.weixin.qq.com/s?tempkey=fixture-{number}'}
    assert calls == ['selected-draft', 'selected-draft']


@pytest.mark.parametrize('value', ['', 'https://mp.weixin.qq.com/', 'https://example.test/s/article',
                                  'javascript:alert(1)', 'https://user:pass@mp.weixin.qq.com/s/article'])
def test_rejects_non_article_urls(monkeypatch, value):
    class Client:
        def __init__(self, _token):
            pass

        def get_draft(self, _media_id):
            return {'news_item': [{'url': value}]}

    monkeypatch.setattr('lazyllm.tools.writer.provider.wechat.WeChatClient', Client)
    monkeypatch.setattr(WeChatWriterProvider, '_access_token', staticmethod(lambda: 'fixture-token'))
    with pytest.raises(DocumentActionError):
        invoke_document_action('builtin:document.wechat_draft_url.v1', 'preview', {'media_id': 'draft'})


@pytest.mark.parametrize('index', [-1, True, '1', 3])
def test_never_falls_back_to_first_article(monkeypatch, index):
    class Client:
        def __init__(self, _token):
            pass

        def get_draft(self, _media_id):
            return {'news_item': [{'url': 'https://mp.weixin.qq.com/s/first-article'}]}

    monkeypatch.setattr('lazyllm.tools.writer.provider.wechat.WeChatClient', Client)
    monkeypatch.setattr(WeChatWriterProvider, '_access_token', staticmethod(lambda: 'fixture-token'))
    with pytest.raises(DocumentActionError):
        invoke_document_action('builtin:document.wechat_draft_url.v1', 'preview',
                               {'media_id': 'draft', 'article_index': index})

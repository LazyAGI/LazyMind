"""Default image discovery is shared by image workflows, PPT and Writer."""
import io

import httpx
import lazyllm
from PIL import Image
import pytest
import requests

from lazymind.chat.engine.tools.infra import image_workflow_support as search
from lazymind.chat.engine.tools import keenable_search
from lazymind.document_tools import writing


@pytest.mark.parametrize('entry', ['legacy', 'batch', 'writer'])
def test_default_image_search_uses_real_commons_files_and_keeps_attribution(monkeypatch, entry):
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    monkeypatch.setattr(keenable_search, '_PUBLIC_RETRY_AT', 0.0)
    image_url = 'https://upload.wikimedia.org/wikipedia/commons/a/ab/solar.jpg?original=kept'
    calls = []

    def request(method, url, **kwargs):
        assert 'X-API-Key' not in kwargs.get('headers', {})
        return httpx.Response(200, request=httpx.Request(method, url), json={'results': [
            {'title': 'An article', 'url': 'https://example.test/article'},
        ]})

    def commons(url, **kwargs):
        calls.append(kwargs['params'])
        return httpx.Response(200, request=httpx.Request('GET', url), json={'query': {'pages': {'1': {
            'title': 'File:Solar panels.jpg', 'index': 1, 'imageinfo': [{
                'url': image_url, 'descriptionurl': 'https://commons.wikimedia.org/wiki/File:Solar_panels.jpg',
                'extmetadata': {'Artist': {'value': '<a>Example photographer</a>'},
                                'LicenseShortName': {'value': 'CC BY-SA 4.0'},
                                'LicenseUrl': {'value': 'https://creativecommons.org/licenses/by-sa/4.0/'}},
            }],
        }}}})

    buffer = io.BytesIO()
    Image.new('RGB', (1000, 800), 'blue').save(buffer, format='JPEG')

    def image_get(url, **kwargs):
        assert url == image_url
        response = requests.Response()
        response.status_code = 200
        response.headers['Content-Type'] = 'image/jpeg'
        response.url = url
        response.raw = io.BytesIO(buffer.getvalue())
        return response

    monkeypatch.setattr(httpx, 'request', request)
    monkeypatch.setattr(httpx, 'get', commons)
    monkeypatch.setattr(requests, 'get', image_get)
    try:
        if entry == 'legacy':
            assert search.image_search_tool('solar panels') == image_url
        elif entry == 'writer':
            result = writing.acquire_web_search_resources({'purpose': 'solar panels'})
            assert result[0]['uri'] == image_url
            assert result[0]['meta']['source_metadata']['license'] == 'CC BY-SA 4.0'
            assert result[0]['meta']['semantic_status'] == 'unverified'
        else:
            result = search.image_search_and_validate('solar panels', target_valid=1)
            assert result['status'] == 'ok'
            assert result['selected_count'] == 1
            assert result['selected'][0]['url'] == image_url
            assert result['selected'][0]['width'] == 1000
            metadata = result['selected'][0]['source_metadata']
            assert metadata['attribution'] == 'Example photographer'
            assert metadata['license'] == 'CC BY-SA 4.0'
            assert metadata['source_page_url'].startswith('https://commons.wikimedia.org/wiki/File:')
        assert len(calls) == 1
        assert calls[0]['gsrnamespace'] == 6
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth

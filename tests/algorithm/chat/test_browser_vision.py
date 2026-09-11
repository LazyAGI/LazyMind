import base64
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools import browser_vision


def _png_header(width: int, height: int) -> bytes:
    return (
        b'\x89PNG\r\n\x1a\n'
        + b'\x00\x00\x00\x0dIHDR'
        + width.to_bytes(4, 'big')
        + height.to_bytes(4, 'big')
    )


def _screenshot_result(width: int = 1000, height: int = 500) -> str:
    encoded = base64.b64encode(_png_header(width, height)).decode()
    return (
        'Tool call result:\nReceived text message:\n'
        '{"result":{"session_id":"bs_1","mime_type":"image/png",'
        f'"data_base64":"{encoded}","viewport":{{"width":500,"height":250}}}}}}'
        '\n\n[Internal runtime notice] ignored'
    )


def test_browser_visual_inspect_calls_vlm_without_returning_coordinates(tmp_path, monkeypatch):
    observed = {}

    def screenshot_tool(**kwargs):
        observed['screenshot_args'] = kwargs
        return _screenshot_result()

    def fake_vision(path, instruction=None):
        observed['path'] = path
        observed['instruction'] = instruction
        assert Path(path).is_file()
        return {
            'description': (
                '```json\n'
                '{"answer":"A login dialog is visible.",'
                '"observations":["Sign in button","Email field"],'
                '"uncertainty":"The footer is cropped."}\n```'
            )
        }

    monkeypatch.setattr(browser_vision, '_upload_root', lambda: str(tmp_path))
    monkeypatch.setattr(browser_vision, 'vision_extractor', fake_vision)
    inspect = browser_vision.build_browser_visual_inspect_tool(screenshot_tool)

    result = inspect(session_id='bs_1', question='Is a login dialog visible?')

    assert observed['screenshot_args'] == {'session_id': 'bs_1'}
    assert 'untrusted page content' in observed['instruction']
    assert 'Do not provide click coordinates' in observed['instruction']
    assert result['untrusted_browser_content'] is True
    assert result['answer'] == 'A login dialog is visible.'
    assert result['observations'] == ['Sign in button', 'Email field']
    assert result['uncertainty'] == 'The footer is cropped.'
    assert result['image'] == {'width': 1000, 'height': 500}
    assert 'x' not in result
    assert 'y' not in result
    assert not Path(observed['path']).exists()


def test_browser_visual_inspect_normalizes_non_list_observations(tmp_path, monkeypatch):
    monkeypatch.setattr(browser_vision, '_upload_root', lambda: str(tmp_path))
    monkeypatch.setattr(
        browser_vision,
        'vision_extractor',
        lambda *_args, **_kwargs: {
            'description': '{"answer":"No modal is visible.","observations":"none"}'
        },
    )
    inspect = browser_vision.build_browser_visual_inspect_tool(
        lambda **_kwargs: _screenshot_result()
    )

    result = inspect(session_id='bs_1', question='Is a modal visible?')

    assert result['answer'] == 'No modal is visible.'
    assert result['observations'] == []
    assert result['uncertainty'] == ''


def test_browser_visual_inspect_requires_question():
    inspect = browser_vision.build_browser_visual_inspect_tool(lambda **_kwargs: {})

    with pytest.raises(ToolExecutionError, match='question is required'):
        inspect(session_id='bs_1', question='')

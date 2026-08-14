from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import sys


SCRIPT_PATH = (
    Path(__file__).resolve().parents[2]
    / "skills"
    / "news"
    / "hot-news-summary"
    / "scripts"
    / "hot_list_fetcher.py"
)


class FakeResponse:
    def __init__(self, payload=None, *, text="", status_code=200):
        self.payload = payload
        self.text = text
        self.status_code = status_code

    def json(self):
        if self.payload is None:
            raise AssertionError("response JSON should not be read")
        return self.payload

    def raise_for_status(self):
        return None


def _load_module(monkeypatch, tmp_path):
    monkeypatch.chdir(tmp_path)
    spec = importlib.util.spec_from_file_location("hot_news_summary_fetcher", SCRIPT_PATH)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    previous = sys.dont_write_bytecode
    sys.dont_write_bytecode = True
    try:
        spec.loader.exec_module(module)
    finally:
        sys.dont_write_bytecode = previous
    return module


def test_score_normalizes_heat_within_each_platform(monkeypatch, tmp_path):
    module = _load_module(monkeypatch, tmp_path)
    items = [
        {"title": "甲一", "platform": "甲", "heat": 100},
        {"title": "甲二", "platform": "甲", "heat": 50},
        {"title": "乙一", "platform": "乙", "heat": 10000},
        {"title": "乙二", "platform": "乙", "heat": 5000},
    ]

    scored = module.score_items(items)
    by_title = {item["title"]: item for item in scored}

    assert by_title["甲一"]["heat_normalized"] == 100
    assert by_title["甲二"]["heat_normalized"] == 50
    assert by_title["乙一"]["heat_normalized"] == 100
    assert by_title["乙二"]["heat_normalized"] == 50


def test_main_emits_the_complete_result_to_stdout(monkeypatch, tmp_path, capsys):
    module = _load_module(monkeypatch, tmp_path)
    items = [
        {
            "title": f"热点 {index}",
            "platform": "测试平台",
            "heat": 100 - index,
            "url": f"https://example.com/{index}",
        }
        for index in range(75)
    ]

    module.FETCHER_REGISTRY = [lambda: ("test", items)]
    monkeypatch.setattr(module, "_save_cache", lambda *_: None)
    monkeypatch.setattr(module, "_archive_history", lambda *_: None)
    monkeypatch.setattr(module.time, "sleep", lambda *_: None)

    module.main()

    output = capsys.readouterr().out
    serialized = output.split("LAZYMIND_RESULT_JSON_BEGIN", 1)[1].split(
        "LAZYMIND_RESULT_JSON_END", 1
    )[0]
    stdout_result = json.loads(serialized)
    file_result = json.loads((tmp_path / "hot_lists.json").read_text(encoding="utf-8"))

    assert stdout_result == file_result
    assert stdout_result["total_items"] == 75
    assert len(stdout_result["data"]) == 75


def test_request_with_retry_forwards_json_body(monkeypatch, tmp_path):
    module = _load_module(monkeypatch, tmp_path)
    calls = []

    def fake_request(method, url, **kwargs):
        calls.append((method, url, kwargs))
        return FakeResponse({})

    monkeypatch.setattr(module.requests, "request", fake_request)
    response = module._request_with_retry(
        "https://example.com/api", method="POST", json_data={"hello": "world"}
    )

    assert response is not None
    assert calls[0][0] == "POST"
    assert calls[0][2]["json"] == {"hello": "world"}


def test_repaired_fetchers_parse_current_response_shapes(monkeypatch, tmp_path):
    module = _load_module(monkeypatch, tmp_path)
    calls = []

    def fake_request(url, **kwargs):
        calls.append((url, kwargs))
        if "weibo.com" in url:
            return FakeResponse(
                {"data": {"realtime": [{"word": "微博热点", "num": 321}]}}
            )
        if "api.zhihu.com" in url:
            return FakeResponse(
                {
                    "data": [
                        {
                            "detail_text": "1234 热度",
                            "target": {
                                "id": 88,
                                "type": "article",
                                "title": "知乎文章",
                            },
                        }
                    ]
                }
            )
        if "douyin.com" in url:
            return FakeResponse(
                {
                    "data": {
                        "word_list": [
                            {"word": "抖音热点", "hot_value": 9, "sentence_id": 42}
                        ]
                    }
                }
            )
        if "top.baidu.com" in url:
            payload = {
                "data": {
                    "cards": [
                        {
                            "content": [
                                {
                                    "content": [
                                        {
                                            "query": "百度热点",
                                            "hotScore": 8,
                                            "url": "https://baidu.example/topic",
                                        }
                                    ]
                                }
                            ]
                        }
                    ]
                }
            }
            return FakeResponse(text=f"<!--s-data:{json.dumps(payload)}-->")
        if "gateway.36kr.com" in url:
            return FakeResponse(
                {
                    "data": {
                        "hotRankList": [
                            {
                                "itemId": 123,
                                "templateMaterial": {
                                    "widgetTitle": "36氪热点",
                                    "statRead": 7,
                                },
                            }
                        ]
                    }
                }
            )
        if "tieba.baidu.com" in url:
            return FakeResponse(
                {
                    "data": {
                        "bang_topic": {
                            "topic_list": [
                                {
                                    "topic_name": "贴吧热点",
                                    "discuss_num": 6,
                                    "topic_url": "https://tieba.example/?a=1&amp;b=2",
                                }
                            ]
                        }
                    }
                }
            )
        raise AssertionError(f"unexpected URL: {url}")

    monkeypatch.setattr(module, "_request_with_retry", fake_request)

    assert module.fetch_weibo()[1][0]["heat"] == 321
    assert module.fetch_zhihu()[1][0]["url"] == "https://zhuanlan.zhihu.com/p/88"
    assert module.fetch_douyin()[1][0]["url"] == "https://www.douyin.com/hot/42"
    assert module.fetch_baidu()[1][0]["title"] == "百度热点"
    assert module.fetch_36kr()[1][0]["url"] == "https://www.36kr.com/p/123"
    assert module.fetch_tieba()[1][0]["url"] == "https://tieba.example/?a=1&b=2"

    kr_call = next(call for call in calls if "gateway.36kr.com" in call[0])
    assert kr_call[1]["method"] == "POST"
    assert kr_call[1]["json_data"]["partner_id"] == "wap"

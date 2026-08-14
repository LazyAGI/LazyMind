#!/usr/bin/env python3
"""
热榜数据抓取脚本 v2.0
- 插件化架构：每个平台一个独立 fetcher，注册即生效
- 8平台覆盖：微博/知乎/B站/抖音/百度/36氪/少数派/贴吧
- 重试 + 降级 + 缓存兜底 + 数据校验
- 结构化评分模型（热度标准化 + 跨平台共振 + 多样性惩罚）
- 历史数据按日归档，为趋势分析提供数据基础
"""

import html
import json
import os
import re
import sys
import time
from datetime import datetime
from typing import Dict, List, Any, Optional, Tuple

import requests

# ============================================================
# 全局配置
# ============================================================

CONFIG = {
    "retry_max": 3,
    "retry_delay": 2,
    "timeout": 15,
    "cache_dir": "./cache",
    "history_dir": "./history",
    "max_per_platform": 50,
    "common_headers": {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
        "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    },
}

for d in [CONFIG["cache_dir"], CONFIG["history_dir"]]:
    os.makedirs(d, exist_ok=True)


# ============================================================
# 工具函数
# ============================================================

def _request_with_retry(
    url: str,
    method: str = "GET",
    headers: Optional[Dict] = None,
    params: Optional[Dict] = None,
    json_data: Optional[Dict] = None,
    max_retry: int = None,
    timeout: int = None,
) -> Optional[requests.Response]:
    """带重试的 HTTP 请求"""
    max_retry = max_retry or CONFIG["retry_max"]
    timeout = timeout or CONFIG["timeout"]
    _headers = {**CONFIG["common_headers"], **(headers or {})}

    for attempt in range(1, max_retry + 1):
        try:
            resp = requests.request(
                method,
                url,
                headers=_headers,
                params=params,
                json=json_data,
                timeout=timeout,
            )
            resp.raise_for_status()
            return resp
        except requests.RequestException as e:
            print(f"  [重试 {attempt}/{max_retry}] {url} -> {e}")
            if attempt < max_retry:
                time.sleep(CONFIG["retry_delay"] * attempt)
    return None


def _normalize_heat(raw) -> float:
    """将各种热度格式统一为 float，用于后续评分"""
    if isinstance(raw, (int, float)):
        return float(raw)
    if isinstance(raw, str):
        raw = raw.strip().replace(",", "").replace("热度", "").replace(" ", "")
        if "万" in raw:
            return float(raw.replace("万", "")) * 10000
        if "亿" in raw:
            return float(raw.replace("亿", "")) * 100000000
        try:
            return float(re.sub(r"[^\d.]", "", raw) or 0)
        except ValueError:
            return 0.0
    return 0.0


def _save_cache(platform: str, data: List[Dict]) -> None:
    """缓存原始抓取结果"""
    path = os.path.join(CONFIG["cache_dir"], f"{platform}.json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump(
            {"timestamp": datetime.now().isoformat(), "data": data},
            f,
            ensure_ascii=False,
            indent=2,
        )


def _load_cache(platform: str, max_age_seconds: int = 3600) -> Optional[List[Dict]]:
    """读取缓存，超过 max_age_seconds 视为过期"""
    path = os.path.join(CONFIG["cache_dir"], f"{platform}.json")
    if not os.path.exists(path):
        return None
    try:
        with open(path, "r", encoding="utf-8") as f:
            cached = json.load(f)
        ts = datetime.fromisoformat(cached["timestamp"])
        if (datetime.now() - ts).total_seconds() > max_age_seconds:
            return None
        print(f"  [{platform}] 使用缓存数据（{len(cached['data'])} 条）")
        return cached["data"]
    except Exception:
        return None


def _archive_history(platform: str, data: List[Dict]) -> None:
    """按日期归档历史数据，供趋势分析使用"""
    today = datetime.now().strftime("%Y-%m-%d")
    dir_path = os.path.join(CONFIG["history_dir"], today)
    os.makedirs(dir_path, exist_ok=True)
    path = os.path.join(dir_path, f"{platform}.json")
    existing = []
    if os.path.exists(path):
        try:
            with open(path, "r", encoding="utf-8") as f:
                existing = json.load(f)
        except Exception:
            pass
    entry = {"timestamp": datetime.now().isoformat(), "data": data}
    existing.append(entry)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(existing, f, ensure_ascii=False, indent=2)


def _validate_item(item: Dict, required_keys: List[str]) -> bool:
    """校验单条数据是否包含必要字段"""
    return all(k in item and item[k] for k in required_keys)


def _fallback_or_cache(platform: str) -> Tuple[str, List[Dict]]:
    """统一降级逻辑：返回缓存，若缓存也没有则返回空列表"""
    cached = _load_cache(platform)
    if cached:
        print(f"  [{platform}] 降级到缓存数据")
        return platform, cached
    print(f"  [{platform}] 所有方案失败，跳过该平台")
    return platform, []


# ============================================================
# 插件化 Fetcher —— 每个平台一个函数
# ============================================================

def fetch_weibo() -> Tuple[str, List[Dict[str, Any]]]:
    """微博热搜"""
    print("[抓取] 微博热搜...")
    url = "https://weibo.com/ajax/side/hotSearch"
    headers = {"Referer": "https://weibo.com", "Accept": "application/json"}
    resp = _request_with_retry(url, headers=headers)
    if not resp:
        return _fallback_or_cache("weibo")
    try:
        data = resp.json()
        items = data.get("data", {}).get("realtime", [])
        result = []
        for item in items[: CONFIG["max_per_platform"]]:
            if item.get("is_ad", 0) == 1:
                continue
            entry = {
                "title": item.get("word", ""),
                "heat": item.get("raw_hot", 0) or item.get("num", 0),
                "label": item.get("word_type", 0),
                "url": f"https://s.weibo.com/weibo?q=%23{item.get('word', '')}%23",
                "platform": "微博热搜",
            }
            if _validate_item(entry, ["title", "heat", "url"]):
                result.append(entry)
        print(f"  微博热搜抓取成功，共 {len(result)} 条")
        return "weibo", result
    except Exception as e:
        print(f"  微博热搜解析失败: {e}")
        return _fallback_or_cache("weibo")


def fetch_zhihu() -> Tuple[str, List[Dict[str, Any]]]:
    """知乎热榜（主API -> 第三方聚合 -> 缓存）"""
    print("[抓取] 知乎热榜...")
    url = "https://api.zhihu.com/topstory/hot-lists/total"
    headers = {
        "Referer": "https://www.zhihu.com/hot",
        "Accept": "application/json",
        "x-api-version": "3.0.40",
    }
    resp = _request_with_retry(url, headers=headers)
    if resp and resp.status_code == 200:
        try:
            data = resp.json()
            items = data.get("data", [])
            result = []
            for item in items[: CONFIG["max_per_platform"]]:
                target = item.get("target", {})
                target_id = str(target.get("id", ""))
                target_type = target.get("type", "question")
                target_url = (
                    f"https://zhuanlan.zhihu.com/p/{target_id}"
                    if target_type == "article"
                    else f"https://www.zhihu.com/question/{target_id}"
                )
                entry = {
                    "title": target.get("title", ""),
                    "heat": _normalize_heat(item.get("detail_text", "")),
                    "excerpt": target.get("excerpt", ""),
                    "url": target_url,
                    "platform": "知乎热榜",
                }
                if _validate_item(entry, ["title", "url"]):
                    result.append(entry)
            if result:
                print(f"  知乎热榜抓取成功，共 {len(result)} 条")
                return "zhihu", result
        except Exception as e:
            print(f"  知乎热榜解析失败: {e}")

    # 降级：第三方聚合
    print("  知乎主API失败，尝试降级方案...")
    try:
        url2 = "https://api.vvhan.com/api/hotlist/zhihuHot"
        resp2 = _request_with_retry(url2)
        if resp2 and resp2.status_code == 200:
            data2 = resp2.json()
            if data2.get("success") and "data" in data2:
                result = []
                for item in data2["data"][: CONFIG["max_per_platform"]]:
                    entry = {
                        "title": item.get("title", ""),
                        "heat": _normalize_heat(item.get("hot", "")),
                        "excerpt": item.get("desc", ""),
                        "url": item.get("url", ""),
                        "platform": "知乎热榜",
                    }
                    if _validate_item(entry, ["title"]):
                        result.append(entry)
                if result:
                    print(f"  知乎热榜（降级）抓取成功，共 {len(result)} 条")
                    return "zhihu", result
    except Exception as e:
        print(f"  知乎降级方案失败: {e}")

    return _fallback_or_cache("zhihu")


def fetch_bilibili() -> Tuple[str, List[Dict[str, Any]]]:
    """B站热门视频"""
    print("[抓取] B站热门...")
    url = "https://api.bilibili.com/x/web-interface/ranking/v2"
    params = {"rid": 0, "type": "all"}
    headers = {"Referer": "https://www.bilibili.com", "Accept": "application/json"}
    resp = _request_with_retry(url, headers=headers, params=params)
    if not resp:
        return _fallback_or_cache("bilibili")
    try:
        data = resp.json()
        items = data.get("data", {}).get("list", [])
        result = []
        for item in items[: CONFIG["max_per_platform"] * 2]:
            stat = item.get("stat", {})
            entry = {
                "title": item.get("title", ""),
                "heat": stat.get("view", 0),
                "author": item.get("owner", {}).get("name", ""),
                "play_count": stat.get("view", 0),
                "danmaku_count": stat.get("danmaku", 0),
                "url": f"https://www.bilibili.com/video/{item.get('bvid', '')}",
                "platform": "B站热门",
            }
            if _validate_item(entry, ["title", "heat", "url"]):
                result.append(entry)
        print(f"  B站热门抓取成功，共 {len(result)} 条")
        return "bilibili", result
    except Exception as e:
        print(f"  B站热门解析失败: {e}")
        return _fallback_or_cache("bilibili")


def fetch_douyin() -> Tuple[str, List[Dict[str, Any]]]:
    """抖音热榜（主API -> 第三方聚合 -> 缓存）"""
    print("[抓取] 抖音热榜...")
    url = "https://www.douyin.com/aweme/v1/web/hot/search/list/"
    params = {"device_platform": "webapp", "aid": "6383"}
    headers = {
        "Referer": "https://www.douyin.com/hot",
        "Accept": "application/json",
    }
    resp = _request_with_retry(url, headers=headers, params=params)
    if resp:
        try:
            data = resp.json()
            word_list = data.get("data", {}).get("word_list", [])
            result = []
            for item in word_list[: CONFIG["max_per_platform"]]:
                sentence_id = str(item.get("sentence_id", ""))
                entry = {
                    "title": item.get("word", ""),
                    "heat": item.get("hot_value", 0),
                    "label": item.get("word_type", ""),
                    "url": f"https://www.douyin.com/hot/{sentence_id}",
                    "platform": "抖音热榜",
                }
                if _validate_item(entry, ["title", "url"]):
                    result.append(entry)
            if result:
                print(f"  抖音热榜抓取成功，共 {len(result)} 条")
                return "douyin", result
        except Exception as e:
            print(f"  抖音热榜解析失败: {e}")

    # 降级
    print("  抖音主API失败，尝试降级方案...")
    try:
        url2 = "https://api.vvhan.com/api/hotlist/douyinHot"
        resp2 = _request_with_retry(url2)
        if resp2 and resp2.status_code == 200:
            data2 = resp2.json()
            if data2.get("success") and "data" in data2:
                result = []
                for item in data2["data"][: CONFIG["max_per_platform"]]:
                    entry = {
                        "title": item.get("title", ""),
                        "heat": _normalize_heat(item.get("hot", "")),
                        "url": item.get("url", ""),
                        "platform": "抖音热榜",
                    }
                    if _validate_item(entry, ["title"]):
                        result.append(entry)
                if result:
                    print(f"  抖音热榜（降级）抓取成功，共 {len(result)} 条")
                    return "douyin", result
    except Exception as e:
        print(f"  抖音降级方案失败: {e}")

    return _fallback_or_cache("douyin")


def fetch_baidu() -> Tuple[str, List[Dict[str, Any]]]:
    """百度热搜"""
    print("[抓取] 百度热搜...")
    url = "https://top.baidu.com/board?tab=realtime"
    headers = {"Referer": "https://top.baidu.com", "Accept": "application/json"}
    resp = _request_with_retry(url, headers=headers)
    if not resp:
        return _fallback_or_cache("baidu")
    try:
        match = re.search(r"<!--s-data:(.*?)-->", getattr(resp, "text", ""), re.S)
        data = json.loads(match.group(1)) if match else resp.json()
        cards = data.get("data", {}).get("cards", [])
        result = []
        for card in cards:
            card_items = card.get("content", [])
            if (
                len(card_items) == 1
                and isinstance(card_items[0], dict)
                and isinstance(card_items[0].get("content"), list)
            ):
                card_items = card_items[0]["content"]
            for item in card_items:
                title = item.get("query", "") or item.get("word", "")
                entry = {
                    "title": title,
                    "heat": item.get("hotScore", 0),
                    "desc": item.get("desc", ""),
                    "url": item.get("url", ""),
                    "platform": "百度热搜",
                }
                if _validate_item(entry, ["title", "url"]):
                    result.append(entry)
        result = result[: CONFIG["max_per_platform"]]
        print(f"  百度热搜抓取成功，共 {len(result)} 条")
        return "baidu", result
    except Exception as e:
        print(f"  百度热搜解析失败: {e}")
        return _fallback_or_cache("baidu")


def fetch_36kr() -> Tuple[str, List[Dict[str, Any]]]:
    """36氪热榜"""
    print("[抓取] 36氪热榜...")
    url = "https://gateway.36kr.com/api/mis/nav/home/nav/rank/hot"
    headers = {
        "Referer": "https://m.36kr.com/hot-list-m",
        "Accept": "application/json",
        "Content-Type": "application/json; charset=utf-8",
    }
    body = {
        "partner_id": "wap",
        "param": {"siteId": 1, "platformId": 2},
        "timestamp": int(time.time() * 1000),
    }
    resp = _request_with_retry(url, method="POST", headers=headers, json_data=body)
    if not resp:
        return _fallback_or_cache("36kr")
    try:
        data = resp.json()
        items = data.get("data", {}).get("hotRankList", [])
        result = []
        for item in items[: CONFIG["max_per_platform"]]:
            material = item.get("templateMaterial", {})
            item_id = str(item.get("itemId", ""))
            entry = {
                "title": material.get("widgetTitle", ""),
                "heat": material.get("statRead", 0),
                "url": f"https://www.36kr.com/p/{item_id}",
                "platform": "36氪",
            }
            if _validate_item(entry, ["title", "url"]):
                result.append(entry)
        print(f"  36氪热榜抓取成功，共 {len(result)} 条")
        return "36kr", result
    except Exception as e:
        print(f"  36氪热榜解析失败: {e}")
        return _fallback_or_cache("36kr")


def fetch_sspai() -> Tuple[str, List[Dict[str, Any]]]:
    """少数派热榜"""
    print("[抓取] 少数派热榜...")
    url = "https://sspai.com/api/v1/article/index/page/get?limit=40&offset=0&type=hot"
    headers = {"Referer": "https://sspai.com", "Accept": "application/json"}
    resp = _request_with_retry(url, headers=headers)
    if not resp:
        return _fallback_or_cache("sspai")
    try:
        data = resp.json()
        items = data.get("data", [])
        result = []
        for item in items[: CONFIG["max_per_platform"]]:
            entry = {
                "title": item.get("title", ""),
                "heat": item.get("like_count", 0) or item.get("view_count", 0),
                "desc": item.get("summary", ""),
                "url": f"https://sspai.com/post/{item.get('id', '')}",
                "platform": "少数派",
            }
            if _validate_item(entry, ["title"]):
                result.append(entry)
        print(f"  少数派热榜抓取成功，共 {len(result)} 条")
        return "sspai", result
    except Exception as e:
        print(f"  少数派热榜解析失败: {e}")
        return _fallback_or_cache("sspai")


def fetch_tieba() -> Tuple[str, List[Dict[str, Any]]]:
    """百度贴吧热议榜"""
    print("[抓取] 贴吧热议榜...")
    url = "https://tieba.baidu.com/hottopic/browse/topicList"
    headers = {"Referer": "https://tieba.baidu.com/hottopic"}
    resp = _request_with_retry(url, headers=headers)
    if not resp:
        return _fallback_or_cache("tieba")
    try:
        data = resp.json()
        items = data.get("data", {}).get("bang_topic", {}).get("topic_list", [])
        result = []
        for item in items[: CONFIG["max_per_platform"]]:
            entry = {
                "title": item.get("topic_name", ""),
                "heat": item.get("discuss_num", 0),
                "desc": item.get("topic_desc", ""),
                "url": html.unescape(item.get("topic_url", "")),
                "platform": "贴吧热议",
            }
            if _validate_item(entry, ["title", "url"]):
                result.append(entry)
        print(f"  贴吧热议抓取成功，共 {len(result)} 条")
        return "tieba", result
    except Exception as e:
        print(f"  贴吧热议解析失败: {e}")
        return _fallback_or_cache("tieba")


# ============================================================
# Fetcher 注册表 —— 新增平台只需写函数 + 注册
# ============================================================

FETCHER_REGISTRY: List = [
    fetch_weibo,
    fetch_zhihu,
    fetch_bilibili,
    fetch_douyin,
    fetch_baidu,
    fetch_36kr,
    fetch_sspai,
    fetch_tieba,
]


# ============================================================
# 结构化评分模型
# ============================================================

def score_items(all_items: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """
    综合得分 = 热度标准化分(40%) + 跨平台共振分(50%) + 基础分(10)
    """
    if not all_items:
        return all_items

    # 1. 热度标准化（0-100）：各平台的热度口径不同，必须分别归一化
    platform_max_heat: Dict[str, float] = {}
    for item in all_items:
        platform = str(item.get("platform", ""))
        heat = _normalize_heat(item.get("heat", 0))
        platform_max_heat[platform] = max(platform_max_heat.get(platform, 0.0), heat)

    for item in all_items:
        platform = str(item.get("platform", ""))
        heat = _normalize_heat(item.get("heat", 0))
        max_heat = platform_max_heat.get(platform, 0.0)
        item["heat_normalized"] = (
            round((heat / max_heat) * 100, 2) if max_heat > 0 else 0
        )

    # 2. 跨平台共振分：标题相似的热点视为同一事件，每多一个平台 +20
    groups: Dict[str, List[Dict]] = {}
    for item in all_items:
        title = item.get("title", "").strip().lower()
        matched = False
        for existing_key in list(groups.keys()):
            # 包含关系 或 4字以上重叠（简易语义近似）
            if title in existing_key or existing_key in title:
                groups[existing_key].append(item)
                matched = True
                break
            overlap = sum(1 for c in title if c in existing_key)
            if overlap >= min(len(title), len(existing_key)) * 0.4 and min(len(title), len(existing_key)) > 0:
                groups[existing_key].append(item)
                matched = True
                break
        if not matched:
            groups[title] = [item]

    for group_items in groups.values():
        platforms = set(it.get("platform", "") for it in group_items)
        resonance_score = len(platforms) * 20
        for it in group_items:
            it["resonance_score"] = resonance_score
            it["cross_platform_count"] = len(platforms)
            it["cross_platforms"] = list(platforms)

    # 3. 综合评分
    for item in all_items:
        item["final_score"] = round(
            item.get("heat_normalized", 0) * 0.4
            + item.get("resonance_score", 0) * 0.5
            + 10,
            2,
        )

    return all_items


# ============================================================
# 主流程
# ============================================================

def main():
    """抓取所有平台热榜，评分，去重，保存"""
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")

    print("=" * 60)
    print("热榜数据抓取 v2.0")
    print(f"时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"平台数: {len(FETCHER_REGISTRY)}")
    print("=" * 60)

    all_items: List[Dict[str, Any]] = []
    platform_stats: Dict[str, int] = {}

    for fetcher in FETCHER_REGISTRY:
        try:
            platform_key, items = fetcher()
            if items:
                _save_cache(platform_key, items)
                _archive_history(platform_key, items)
            platform_stats[platform_key] = len(items)
            all_items.extend(items)
            time.sleep(0.5)
        except Exception as e:
            print(f"  [{fetcher.__name__}] 异常: {e}")
            platform_stats[fetcher.__name__] = 0

    # 评分
    print("\n[评分] 结构化评分中...")
    all_items = score_items(all_items)
    all_items.sort(key=lambda x: x.get("final_score", 0), reverse=True)

    result = {
        "version": "2.0",
        "timestamp": datetime.now().isoformat(),
        "total_items": len(all_items),
        "platform_stats": platform_stats,
        "data": all_items,
    }

    output_file = "./hot_lists.json"
    with open(output_file, "w", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)

    print("\n" + "=" * 60)
    print(f"数据已保存到: {output_file}")
    print(f"总计: {len(all_items)} 条")
    for k, v in platform_stats.items():
        print(f"  - {k}: {v} 条")
    print("=" * 60)

    # LazyMind 的 run_script 会把 stdout 交给智能体。文件仍完整保留，
    # 同时输出同一份完整 JSON，避免沙箱中的临时文件在后续步骤中不可见。
    print("LAZYMIND_RESULT_JSON_BEGIN")
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))
    print("LAZYMIND_RESULT_JSON_END")


if __name__ == "__main__":
    main()

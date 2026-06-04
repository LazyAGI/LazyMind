from __future__ import annotations

import inspect
from dataclasses import dataclass
from typing import Any

import docstring_parser
from lazyllm.tools.fs.supplier.feishu import FeishuFS
from lazyllm.tools.tools.search import ArxivSearch, BingSearch, BochaSearch, GoogleSearch, WikipediaSearch

from lazymind.chat.engine.tools import (
    KBToolGroup,
    TempKBToolGroup,
    calculator,
    memory_editor,
    skill_editor,
    url_fetch,
    vision_extractor,
    vocab_learn,
)


@dataclass
class ToolGroupConfig:
    name: str
    label: str
    description: str
    instance: Any


SKILL_TOOL_GROUP = ToolGroupConfig(
    name='skill',
    label='技能工具',
    description='利用已安装的技能进行查询、读文件、执行脚本',
    instance=None,
)


DEFAULT_TOOLS: list[ToolGroupConfig] = [
    ToolGroupConfig(
        name='kb',
        label='知识库检索',
        description='从知识库中搜索文档，支持语义检索、关键词检索、上下文窗口等',
        instance=KBToolGroup(),
    ),
    ToolGroupConfig(
        name='temp_kb',
        label='临时文件检索',
        description='从用户上传的临时文件中搜索相关内容',
        instance=TempKBToolGroup(),
    ),
    ToolGroupConfig(
        name='calculator',
        label='科学计算器',
        description='安全地执行数学表达式计算',
        instance=calculator,
    ),
    ToolGroupConfig(
        name='wikipedia',
        label='Wikipedia 搜索',
        description='从 Wikipedia 搜索知识条目',
        instance=WikipediaSearch(skip_auth=True),
    ),
    ToolGroupConfig(
        name='arxiv',
        label='Arxiv 论文搜索',
        description='从 Arxiv 搜索学术论文',
        instance=ArxivSearch(skip_auth=True),
    ),
    ToolGroupConfig(
        name='google',
        label='Google 搜索',
        description='使用 Google 搜索引擎检索互联网内容',
        instance=GoogleSearch(),
    ),
    ToolGroupConfig(
        name='bing',
        label='Bing 搜索',
        description='使用 Bing 搜索引擎检索互联网内容',
        instance=BingSearch(),
    ),
    ToolGroupConfig(
        name='bocha',
        label='Bocha 搜索',
        description='使用 Bocha 搜索引擎检索互联网内容',
        instance=BochaSearch(),
    ),
    ToolGroupConfig(
        name='url_fetch',
        label='网页抓取',
        description='获取并解析公开网页的可读内容',
        instance=url_fetch,
    ),
    ToolGroupConfig(
        name='multimodal',
        label='多模态识别',
        description='从图片中提取文字描述',
        instance=vision_extractor,
    ),
    ToolGroupConfig(
        name='vocab_learn',
        label='词汇学习',
        description='学习用户专属的词汇映射和同义词',
        instance=vocab_learn,
    ),
    ToolGroupConfig(
        name='memory_editor',
        label='记忆编辑',
        description='记录和编辑跨会话的用户记忆和偏好',
        instance=memory_editor,
    ),
    ToolGroupConfig(
        name='skill_editor',
        label='技能编辑',
        description='创建、修改和删除技能',
        instance=skill_editor,
    ),
    ToolGroupConfig(
        name='feishu',
        label='飞书文件系统',
        description='浏览和管理飞书云文档',
        instance=FeishuFS(space_id='dynamic', dynamic_auth=True),
    ),
]


def _resolve_method_name(instance: Any, method_name: str) -> str:
    if method_name == '__call__':
        return instance.__class__.__name__
    return method_name


def _extract_methods(instance: Any) -> list[dict]:
    public_apis = getattr(instance, '__public_apis__', None)
    if public_apis is not None:
        methods = []
        for method_name in public_apis:
            resolved_name = _resolve_method_name(instance, method_name)
            method = getattr(instance, method_name, None)
            if method is None:
                methods.append({'name': resolved_name, 'summary': ''})
                continue
            try:
                doc = inspect.getdoc(method)
                summary = docstring_parser.parse(doc).short_description if doc else ''
            except Exception:
                summary = ''
            methods.append({'name': resolved_name, 'summary': summary})
        return methods

    if callable(instance):
        name = getattr(instance, '__name__', '')
        try:
            doc = inspect.getdoc(instance)
            summary = docstring_parser.parse(doc).short_description if doc else ''
        except Exception:
            summary = ''
        return [{'name': name, 'summary': summary}]

    return []


_SKILL_METHODS = [
    {'name': 'get_skill', 'summary': 'Get the full usage for a skill (SKILL.md).'},
    {'name': 'read_reference', 'summary': 'Read a reference file within a skill directory.'},
    {'name': 'run_script', 'summary': 'Run a script within a skill directory.'},
]


def get_all_tool_groups() -> list[dict]:
    result = []
    for cfg in DEFAULT_TOOLS:
        methods = _extract_methods(cfg.instance)
        result.append({
            'name': cfg.name,
            'label': cfg.label,
            'description': cfg.description,
            'methods': methods,
            'can_disable': True,
        })
    result.append({
        'name': SKILL_TOOL_GROUP.name,
        'label': SKILL_TOOL_GROUP.label,
        'description': SKILL_TOOL_GROUP.description,
        'methods': _SKILL_METHODS,
        'can_disable': False,
    })
    return result


def group_is_active(cfg: ToolGroupConfig) -> bool:
    key_source = getattr(cfg.instance, '__key_source__', None)
    if key_source is None:
        return True
    try:
        return bool(key_source())
    except Exception:
        return False


def filter_tools(
    configs: list[ToolGroupConfig],
    available_tools: list[str] | None = None,
) -> list[ToolGroupConfig]:
    result = []
    for cfg in configs:
        if available_tools is not None and cfg.name not in available_tools:
            continue
        if not group_is_active(cfg):
            continue
        result.append(cfg)
    return result

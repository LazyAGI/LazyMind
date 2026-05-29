from __future__ import annotations

import inspect
from dataclasses import dataclass
from typing import Any, Callable

import docstring_parser
import lazyllm
from lazyllm.tools.fs.supplier.feishu import FeishuFS
from lazyllm.tools.tools.search import ArxivSearch, BingSearch, BochaSearch, GoogleSearch, WikipediaSearch

from lazymind.chat.engine.tools import (
    CalculatorToolGroup,
    KBToolGroup,
    MemoryToolGroup,
    MultimodalToolGroup,
    SkillManagerToolGroup,
    TempKBToolGroup,
    VocabToolGroup,
    UrlFetchToolGroup,
)


@dataclass
class ToolGroupConfig:
    name: str
    label: str
    description: str
    instance: Any
    key_source: Callable[[Any], Any] | None = None


def _dynamic_key(namespace: str, name: str) -> str:
    try:
        mapping = lazyllm.globals.config[namespace] or {}
    except Exception:
        return ''
    r = (mapping.get(name) or '').strip()
    lazyllm.LOG.debug(f'get dynamic key {namespace}.{name}: {r}')
    return r


def _feishu_key_source(_instance) -> str:
    return _dynamic_key('dynamic_fs_auth', 'feishu')


def _google_key_source(_instance) -> str:
    return _dynamic_key('dynamic_tool_auth', 'google')


def _bing_key_source(_instance) -> str:
    return _dynamic_key('dynamic_tool_auth', 'bing')


def _bocha_key_source(_instance) -> str:
    return _dynamic_key('dynamic_tool_auth', 'bocha')


def _memory_key_source(_instance) -> str:
    return lazyllm.globals['agentic_config'].get('use_memory', True)


def _kb_key_source(_instance) -> str:
    return lazyllm.globals['agentic_config'].get('filters', {}).get('kb_id')


def _temp_kb_key_source(_instance) -> str:
    return lazyllm.globals['agentic_config'].get('files')


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
        key_source=_kb_key_source,
    ),
    ToolGroupConfig(
        name='temp_kb',
        label='临时文件检索',
        description='从用户上传的临时文件中搜索相关内容',
        instance=TempKBToolGroup(),
        key_source=_temp_kb_key_source,
    ),
    ToolGroupConfig(
        name='calculator',
        label='科学计算器',
        description='安全地执行数学表达式计算',
        instance=CalculatorToolGroup(),
    ),
    ToolGroupConfig(
        name='wikipedia',
        label='Wikipedia 搜索',
        description='从 Wikipedia 搜索知识条目',
        instance=WikipediaSearch(),
    ),
    ToolGroupConfig(
        name='arxiv',
        label='Arxiv 论文搜索',
        description='从 Arxiv 搜索学术论文',
        instance=ArxivSearch(),
    ),
    ToolGroupConfig(
        name='google',
        label='Google 搜索',
        description='使用 Google 搜索引擎检索互联网内容',
        instance=GoogleSearch(dynamic_auth=True),
        key_source=_google_key_source,
    ),
    ToolGroupConfig(
        name='bing',
        label='Bing 搜索',
        description='使用 Bing 搜索引擎检索互联网内容',
        instance=BingSearch(dynamic_auth=True),
        key_source=_bing_key_source,
    ),
    ToolGroupConfig(
        name='bocha',
        label='Bocha 搜索',
        description='使用 Bocha 搜索引擎检索互联网内容',
        instance=BochaSearch(dynamic_auth=True),
        key_source=_bocha_key_source,
    ),
    ToolGroupConfig(
        name='url_fetch',
        label='网页抓取',
        description='获取并解析公开网页的可读内容',
        instance=UrlFetchToolGroup(),
    ),
    ToolGroupConfig(
        name='multimodal',
        label='多模态识别',
        description='从图片中提取文字描述',
        instance=MultimodalToolGroup(),
    ),
    ToolGroupConfig(
        name='vocab',
        label='词汇管理',
        description='管理用户专属的词汇映射和同义词',
        instance=VocabToolGroup(),
    ),
    ToolGroupConfig(
        name='memory',
        label='记忆管理',
        description='记录和管理跨会话的用户记忆和偏好',
        instance=MemoryToolGroup(),
        key_source=_memory_key_source,
    ),
    ToolGroupConfig(
        name='skill_manager',
        label='技能管理',
        description='创建、修改和删除技能',
        instance=SkillManagerToolGroup(),
    ),
    ToolGroupConfig(
        name='feishu',
        label='飞书文件系统',
        description='浏览和管理飞书云文档',
        instance=FeishuFS(space_id='dynamic', dynamic_auth=True),
        key_source=_feishu_key_source,
    ),
]


def _resolve_method_name(instance: Any, method_name: str) -> str:
    if method_name == '__call__':
        return instance.__class__.__name__
    return method_name


def _extract_methods(instance: Any) -> list[dict]:
    methods = []
    for method_name in instance.__public_apis__:
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
    if cfg.key_source is None:
        return True
    try:
        return bool(cfg.key_source(cfg.instance))
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


def to_agent_inputs(configs: list[ToolGroupConfig]) -> list[Any]:
    return [cfg.instance for cfg in configs]

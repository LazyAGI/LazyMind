from __future__ import annotations

import inspect
from dataclasses import dataclass
from typing import Any, Callable

import docstring_parser
import lazyllm
from lazyllm.tools.fs.supplier.feishu import FeishuFS
from lazyllm.tools.fs.supplier.notion import NotionFS
from lazyllm.tools.tools.search import (
    ArxivSearch,
    BingSearch,
    BochaSearch,
    GoogleSearch,
    SciverseSearch,
    TavilySearch,
    WikipediaSearch,
)

from lazymind.chat.engine.tools import (
    KBToolGroup,
    LocalFSToolGroup,
    calculator,
    image_editor,
    image_generator,
    kb_tmp_search,
    memory_editor,
    read_memory,
    skill_editor,
    url_fetch,
    vision_extractor,
    vocab_learn,
)
from lazymind.model_config import is_model_role_available


@dataclass
class ToolGroupConfig:
    name: str
    label: str
    description: str
    instance: Any
    model_role: str | None = None
    key_source: Callable[[], Any] | None = None
    pick_first_valid: bool = False

    def __post_init__(self) -> None:
        if self.pick_first_valid and not isinstance(self.instance, (list, tuple)):
            raise TypeError(
                'instance must be a list or tuple when pick_first_valid is True, '
                f'got {type(self.instance).__name__}'
            )


_WEB_SEARCH_ENGINE_INSTANCES: list = [
    GoogleSearch(),
    BingSearch(),
    BochaSearch(),
    TavilySearch(),
]

_ACADEMIC_SEARCH_ENGINE_INSTANCES: list = [
    SciverseSearch(),
    ArxivSearch(skip_auth=True),
]


def _temp_kb_key_source() -> Any:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    return agentic_config.get('files')


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
        instance=kb_tmp_search,
        key_source=_temp_kb_key_source,
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
        name='web_search',
        label='网页搜索',
        description='使用搜索引擎检索互联网内容，自动选择可用的搜索服务',
        instance=_WEB_SEARCH_ENGINE_INSTANCES,
        pick_first_valid=True,
    ),
    ToolGroupConfig(
        name='academic_search',
        label='学术搜索',
        description='搜索学术论文和科学文献，自动选择可用的学术搜索服务',
        instance=_ACADEMIC_SEARCH_ENGINE_INSTANCES,
        pick_first_valid=True,
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
        model_role='vlm',
    ),
    ToolGroupConfig(
        name='image_generator',
        label='文生图',
        description='根据文字描述生成图片',
        instance=image_generator,
        model_role='image_generator',
    ),
    ToolGroupConfig(
        name='image_editor',
        label='图编辑',
        description='根据文字指令编辑参考图片',
        instance=image_editor,
        model_role='image_editor',
    ),
    ToolGroupConfig(
        name='vocab_learn',
        label='词汇学习',
        description='学习用户专属的词汇映射和同义词',
        instance=vocab_learn,
    ),
    ToolGroupConfig(
        name='read_memory',
        label='记忆读取',
        description='读取当前的用户记忆或偏好内容',
        instance=read_memory,
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
        name='local_fs',
        label='本地文件',
        description='在配置的本地路径内进行 glob 匹配、grep 搜索、文件读取（只读）',
        instance=LocalFSToolGroup(),
    ),
    ToolGroupConfig(
        name='feishu',
        label='飞书文件系统',
        description='浏览和管理飞书云文档',
        instance=FeishuFS(space_id='dynamic', dynamic_auth=True),
    ),
    ToolGroupConfig(
        name='notion',
        label='Notion 文件系统',
        description='浏览、搜索和管理 Notion 页面',
        instance=NotionFS(dynamic_auth=True),
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


def _extract_group_methods(instances: list) -> list[dict]:
    methods = []
    for inst in instances:
        name = inst.__class__.__name__
        try:
            doc = inspect.getdoc(inst)
            summary = docstring_parser.parse(doc).short_description if doc else ''
        except Exception:
            summary = ''
        methods.append({
            'name': name,
            'summary': summary,
            'active': _instance_is_active(inst),
        })
    return methods


_SKILL_METHODS = [
    {'name': 'get_skill', 'summary': 'Get the full usage for a skill (SKILL.md).'},
    {'name': 'read_reference', 'summary': 'Read a reference file within a skill directory.'},
    {'name': 'run_script', 'summary': 'Run a script within a skill directory.'},
]


def _instance_is_active(instance: Any) -> bool:
    key_source = getattr(instance, '__key_source__', None)
    if key_source is None:
        return True
    return _key_source_is_active(key_source)


def _key_source_is_active(key_source: Callable[[], Any]) -> bool:
    try:
        return bool(key_source())
    except Exception:
        return False


def group_is_active(cfg: ToolGroupConfig) -> bool:
    if cfg.model_role and not is_model_role_available(cfg.model_role):
        return False
    if cfg.key_source and not _key_source_is_active(cfg.key_source):
        return False
    if cfg.pick_first_valid:
        return any(_instance_is_active(inst) for inst in cfg.instance)
    if cfg.instance is None:
        return True
    result = _instance_is_active(cfg.instance)
    if cfg.name == 'kb':
        from lazyllm import LOG as _LOG
        _LOG.info(f'[KBToolGroup_DEBUG] group_is_active kb={result!r}')
    return result


def get_all_tool_groups() -> list[dict]:
    result = []
    for cfg in DEFAULT_TOOLS:
        if cfg.pick_first_valid:
            methods = _extract_group_methods(cfg.instance)
        else:
            methods = _extract_methods(cfg.instance)
        result.append({
            'name': cfg.name,
            'label': cfg.label,
            'description': cfg.description,
            'methods': methods,
            'can_disable': True,
            'active': group_is_active(cfg),
        })
    result.append({
        'name': SKILL_TOOL_GROUP.name,
        'label': SKILL_TOOL_GROUP.label,
        'description': SKILL_TOOL_GROUP.description,
        'methods': _SKILL_METHODS,
        'can_disable': False,
        'active': True,
    })
    return result


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


def build_agent_tools(configs: list[ToolGroupConfig]) -> list:
    result = []
    for cfg in configs:
        if cfg.pick_first_valid:
            group = dict(
                name=cfg.name,
                desc=cfg.description,
                pick_first_valid=True,
                tools=list(cfg.instance),
            )
            if cfg.key_source:
                group['key_source'] = cfg.key_source
            result.append(group)
        elif cfg.key_source:
            result.append((cfg.instance, cfg.key_source))
        else:
            result.append(cfg.instance)
    return result

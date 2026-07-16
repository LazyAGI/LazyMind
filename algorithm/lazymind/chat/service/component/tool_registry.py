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
    KBToolkit,
    ExternalDatabaseToolkit,
    LocalFileToolkit,
    WriterCreateToolkit,
    WriterRevisionToolkit,
    calculator,
    image_editor,
    image_generator,
    kb_tmp_search,
    SkillManagementToolkit,
    list_data_sources,
    build_schedule_toolkit,
    url_fetch,
    video_generator,
    video_to_gif,
    vision_extractor,
    vocab_learn,
)
from lazymind.model_config import is_model_role_available

SystemPromptAppendix = dict[str, str | tuple[str, ...]]
SYSTEM_PROMPT_APPENDIX_SECTIONS = ('tool_policy', 'safety', 'output_contract', 'response_policy')

IMAGE_MARKDOWN_OUTPUT_APPENDIX: SystemPromptAppendix = {
    'output_contract': (
        'When showing an image, copy `image_markdown` from the tool result verbatim when available. '
        'Otherwise use the returned `image_url` or signed `/static-files/` text exactly with Markdown '
        'image syntax. Never invent a host, URL prefix, CDN/tool-output URL, or rewrite a signed '
        '`/static-files/` path as an HTTP URL. Never expose a bare local filesystem path.',
    ),
}
VIDEO_MARKDOWN_OUTPUT_APPENDIX: SystemPromptAppendix = {
    'output_contract': (
        'When a tool result contains `video_markdown`, copy it verbatim into the final answer '
        '(or use `video_url` when markdown is absent). Do not invent or rewrite signed URLs.',
    ),
}
KNOWLEDGE_CITATION_OUTPUT_APPENDIX: SystemPromptAppendix = {
    'output_contract': (
        'When the answer uses evidence returned by a knowledge-base tool, preserve its citation '
        'markers exactly and cite the supporting evidence in the answer. Never invent, rewrite, or '
        'fabricate a knowledge-base citation marker. For web, URL-fetch, or academic evidence, cite '
        'the source title or URL plainly instead of fabricating a knowledge-base marker.',
    ),
}


@dataclass
class ToolConfig:
    name: str
    label: str
    description: str
    tool: Any
    module: str
    label_en: str = ''
    description_en: str = ''
    model_role: str | None = None
    key_source: Callable[[], Any] | None = None
    pick_first_valid: bool = False
    capability_id: str = ''
    equivalence_scope: str = 'infrastructure'
    provider_id: str = ''
    product_id: str = ''
    input_schema: dict[str, Any] | None = None
    output_schema: dict[str, Any] | None = None
    required_config: list[str] | None = None
    appendix_system_prompt: SystemPromptAppendix | None = None

    def __post_init__(self) -> None:
        if self.pick_first_valid and not isinstance(self.tool, dict):
            raise TypeError(
                'tool must be a provider toolkit dict when pick_first_valid is True, '
                f'got {type(self.tool).__name__}'
            )
        for section, values in (self.appendix_system_prompt or {}).items():
            if section not in SYSTEM_PROMPT_APPENDIX_SECTIONS:
                raise ValueError(
                    f'unsupported appendix_system_prompt section {section!r}; '
                    f'expected one of {SYSTEM_PROMPT_APPENDIX_SECTIONS}'
                )
            entries = (values,) if isinstance(values, str) else values
            if not isinstance(entries, tuple) or not all(isinstance(item, str) for item in entries):
                raise TypeError(
                    'appendix_system_prompt values must be a string or tuple of strings'
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


class WikipediaToolkit(WikipediaSearch):
    """Search Wikipedia, then fetch one or more result contents when needed."""


_CLOUD_FILE_TOOLKIT = {
    'name': 'CloudFileToolkit',
    'desc': (
        'Authenticated cloud files and documents. Use this Toolkit for Feishu/Lark '
        'Wiki or Docs links (including *.feishu.cn/wiki/*), Notion links, and paths '
        'inside connected cloud services; do not send those URLs to url_fetch. '
        'Expand this Toolkit, choose the supplier that owns the URL or path, then '
        'expand that supplier Toolkit and select its resolve, read, search, browse, '
        'or write tool.'
    ),
    'tools': [
        FeishuFS(space_id='dynamic', dynamic_auth=True),
        NotionFS(dynamic_auth=True),
    ],
    'lazy': True,
}


def _temp_kb_key_source() -> Any:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    return agentic_config.get('files')


SKILL_TOOL_CONFIG = ToolConfig(
    name='skill',
    label='技能工具',
    description='利用已安装的技能进行查询、读文件、执行脚本',
    tool=None,
    module='personalization',
    label_en='Skills',
    description_en='Use installed skills to search, read files, and run scripts.',
)

DEFAULT_TOOLS: list[ToolConfig] = [
    ToolConfig(
        name='kb',
        label='知识库',
        description='发现知识库、查询文档与统计，并进行语义、关键词和上下文检索',
        tool=KBToolkit(), module='retrieval',
        label_en='Knowledge Base',
        description_en='Discover knowledge bases, inspect documents and statistics, and retrieve their content.',
        capability_id='knowledge_base_search',
        input_schema={'query': 'string'}, output_schema={'results': 'list'}, required_config=['knowledge_base'],
        appendix_system_prompt={
            'output_contract': (
                *IMAGE_MARKDOWN_OUTPUT_APPENDIX['output_contract'],
                *KNOWLEDGE_CITATION_OUTPUT_APPENDIX['output_contract'],
            ),
        },
    ),
    ToolConfig(
        name='temp_kb',
        label='临时文件检索',
        description='从用户上传的临时文件中搜索相关内容',
        tool=(kb_tmp_search, _temp_kb_key_source), module='retrieval',
        label_en='Temporary File Search',
        description_en='Search relevant content in temporary files uploaded by the user.',
        key_source=_temp_kb_key_source,
        appendix_system_prompt=KNOWLEDGE_CITATION_OUTPUT_APPENDIX,
    ),
    ToolConfig(
        name='data_sources', label='数据源查询', description='查询已配置的数据源服务',
        tool=list_data_sources, module='data', label_en='Data Sources',
        description_en='List configured data-source provider services.',
    ),
    ToolConfig(
        name='external_db',
        label='外部数据库查询',
        description='只读查看已配置外部数据库 schema，并执行只读 SELECT/WITH 查询',
        tool=ExternalDatabaseToolkit(), module='data',
        label_en='External Database Query',
        description_en='Inspect configured external database schemas and run read-only SELECT or WITH queries.',
    ),
    ToolConfig(
        name='writer_create', label='AI 写作',
        description='从资料画像和大纲构建章节草稿与最终成稿',
        tool=WriterCreateToolkit(), module='content', label_en='AI Writing',
        description_en='Create structured long-form writing from source material.',
    ),
    ToolConfig(
        name='writer_revision', label='AI 修订', description='结构化定位、规划和修改已有草稿',
        tool=WriterRevisionToolkit(), module='content', label_en='AI Revision',
        description_en='Revise existing drafts through a validated patch workflow.',
    ),
    ToolConfig(
        name='calculator',
        label='科学计算器',
        description='安全地执行数学表达式计算',
        tool=calculator, module='utility',
        label_en='Scientific Calculator',
        description_en='Safely evaluate mathematical expressions.',
    ),
    ToolConfig(
        name='wikipedia',
        label='Wikipedia 搜索',
        description='从 Wikipedia 搜索知识条目',
        tool=WikipediaToolkit(skip_auth=True), module='retrieval',
        label_en='Wikipedia Search',
        description_en='Search Wikipedia knowledge entries.',
    ),
    ToolConfig(
        name='web_search',
        label='网页搜索',
        description='使用搜索引擎检索互联网内容，自动选择可用的搜索服务',
        tool={
            'name': 'WebSearchToolkit',
            'desc': (
                'Search the web with the first available provider. Each search query must represent '
                'one search intent; issue separate calls for unrelated topics. Use get_content or '
                'get_contents when result snippets are insufficient.'
            ),
            'pick_first_valid': True,
            'tools': _WEB_SEARCH_ENGINE_INSTANCES,
        },
        module='retrieval',
        label_en='Web Search',
        description_en='Search the internet using the first available search provider.',
        pick_first_valid=True,
        capability_id='web_search',
        equivalence_scope='provider_bound',
        input_schema={'query': 'string'}, output_schema={'results': 'list'}, required_config=['search_provider'],
    ),
    ToolConfig(
        name='academic_search',
        label='学术搜索',
        description='搜索学术论文和科学文献，自动选择可用的学术搜索服务',
        tool={
            'name': 'AcademicSearchToolkit',
            'desc': (
                'Search papers, authors, abstracts, and scholarly metadata with the first available '
                'provider. Use this instead of general web search for academic questions, and fetch '
                'content only after identifying the relevant paper.'
            ),
            'pick_first_valid': True,
            'tools': _ACADEMIC_SEARCH_ENGINE_INSTANCES,
        },
        module='retrieval',
        label_en='Academic Search',
        description_en='Search academic papers and scientific literature using the first available provider.',
        pick_first_valid=True,
        capability_id='academic_search',
        equivalence_scope='provider_bound',
        input_schema={'query': 'string'}, output_schema={'papers': 'list'}, required_config=['academic_search_provider'],
    ),
    ToolConfig(
        name='url_fetch',
        label='网页抓取',
        description='获取并解析公开网页的可读内容',
        tool=url_fetch, module='retrieval',
        label_en='Web Page Fetch',
        description_en='Fetch and parse readable content from public web pages.',
    ),
    ToolConfig(
        name='multimodal',
        label='多模态识别',
        description='从图片中提取文字描述',
        tool=vision_extractor, module='content',
        label_en='Multimodal Recognition',
        description_en='Extract text descriptions from images.',
        model_role='vlm',
    ),
    ToolConfig(
        name='image_generator',
        label='文生图',
        description='根据文字描述生成图片',
        tool=image_generator, module='content',
        label_en='Image Generation',
        description_en='Generate images from text descriptions.',
        model_role='image_generator',
        capability_id='image_generation',
        input_schema={'prompt': 'string'}, output_schema={'image': 'file'}, required_config=['image_generator_model'],
        appendix_system_prompt=IMAGE_MARKDOWN_OUTPUT_APPENDIX,
    ),
    ToolConfig(
        name='image_editor',
        label='图编辑',
        description='根据文字指令编辑参考图片',
        tool=image_editor, module='content',
        label_en='Image Editing',
        description_en='Edit reference images using text instructions.',
        model_role='image_editor',
        capability_id='image_editing',
        appendix_system_prompt=IMAGE_MARKDOWN_OUTPUT_APPENDIX,
    ),
    ToolConfig(
        name='video_generator',
        label='文生视频',
        description='根据文字描述生成视频，可选首帧参考图；同轮多次调用并行，视频侧最多同时3路',
        tool=video_generator, module='content',
        model_role='video_generator',
        capability_id='video_generation',
        input_schema={'prompt': 'string'}, output_schema={'video': 'file'},
        required_config=['video_generator_model'],
        appendix_system_prompt=VIDEO_MARKDOWN_OUTPUT_APPENDIX,
    ),
    ToolConfig(
        name='video_to_gif',
        label='视频转GIF',
        description='将本地视频转换为 GIF 动图；同轮多次调用并行，GIF 侧最多同时3路',
        tool=video_to_gif, module='content',
        capability_id='video_to_gif',
        input_schema={'url': 'string'}, output_schema={'image': 'file'},
        appendix_system_prompt=IMAGE_MARKDOWN_OUTPUT_APPENDIX,
    ),
    ToolConfig(
        name='vocab_learn',
        label='词汇学习',
        description='学习用户专属的词汇映射和同义词',
        tool=vocab_learn, module='personalization',
        label_en='Vocabulary Learning',
        description_en='Learn user-specific vocabulary mappings and synonyms.',
    ),
    ToolConfig(
        name='skill_editor',
        label='技能编辑',
        description='创建、修改和删除技能',
        tool=SkillManagementToolkit(), module='personalization',
        label_en='Skill Editing',
        description_en='Create, update, and delete skills.',
    ),
    ToolConfig(
        name='local_fs',
        label='本地文件',
        description='在配置的本地路径内进行 glob 匹配、grep 搜索、文件读取（只读）',
        tool=LocalFileToolkit(), module='data',
        label_en='Local Files',
        description_en='Run glob matching, grep searches, and read-only file access within configured local paths.',
    ),
    ToolConfig(
        name='cloud_files', label='云文件', description='浏览、搜索和管理已连接的云文件系统',
        tool=_CLOUD_FILE_TOOLKIT,
        module='data', label_en='Cloud Files',
        description_en='Read and manage authenticated Feishu Wiki, Feishu Docs, Notion, and other cloud files.',
    ),
    ToolConfig(
        name='schedule', label='定时任务', description='创建、查询、修改、取消和立即触发定时任务',
        tool=build_schedule_toolkit(), module='execution', label_en='Schedules',
        description_en='Create, inspect, update, cancel, and trigger recurring schedules.',
    ),
]


def _resolve_method_name(instance: Any, method_name: str) -> str:
    if method_name == '__call__':
        return instance.__class__.__name__
    return method_name


def _extract_methods(instance: Any) -> list[dict]:
    if isinstance(instance, dict):
        return _extract_group_methods(instance.get('tools', []))
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


def _registration_target(tool: Any) -> Any:
    if isinstance(tool, (tuple, list)) and len(tool) == 2:
        return tool[0]
    return tool


def tool_is_active(cfg: ToolConfig) -> bool:
    if cfg.model_role and not is_model_role_available(cfg.model_role):
        return False
    if cfg.key_source and not _key_source_is_active(cfg.key_source):
        return False
    if cfg.pick_first_valid:
        return any(_instance_is_active(inst) for inst in cfg.tool.get('tools', []))
    target = _registration_target(cfg.tool)
    if target is None:
        return True
    if isinstance(target, dict):
        return any(_instance_is_active(inst) for inst in target.get('tools', []))
    return _instance_is_active(target)


def normalize_tool_locale(locale: str | None) -> str:
    for part in (locale or '').split(','):
        tag = part.split(';', 1)[0].strip().lower()
        if tag == 'zh' or tag.startswith('zh-'):
            return 'zh-CN'
        if tag == 'en' or tag.startswith('en-'):
            return 'en-US'
    return 'zh-CN'


def get_all_tool_groups(locale: str | None = None) -> list[dict]:
    use_english = normalize_tool_locale(locale) == 'en-US'
    result = []
    for cfg in DEFAULT_TOOLS:
        if cfg.pick_first_valid:
            methods = _extract_group_methods(cfg.tool.get('tools', []))
        else:
            methods = _extract_methods(_registration_target(cfg.tool))
        result.append({
            'name': cfg.name,
            'label': cfg.label_en or cfg.label if use_english else cfg.label,
            'description': cfg.description_en or cfg.description if use_english else cfg.description,
            'methods': methods,
            'can_disable': True,
            'active': tool_is_active(cfg),
            'module': cfg.module,
            'capability_id': cfg.capability_id or cfg.name,
            'equivalence_scope': cfg.equivalence_scope,
            'provider_id': cfg.provider_id,
            'product_id': cfg.product_id,
            'input_schema': cfg.input_schema or {},
            'output_schema': cfg.output_schema or {},
            'required_config': cfg.required_config or [],
        })
    result.append({
        'name': SKILL_TOOL_CONFIG.name,
        'label': SKILL_TOOL_CONFIG.label_en or SKILL_TOOL_CONFIG.label if use_english else SKILL_TOOL_CONFIG.label,
        'description': (
            SKILL_TOOL_CONFIG.description_en or SKILL_TOOL_CONFIG.description
            if use_english else SKILL_TOOL_CONFIG.description
        ),
        'methods': _SKILL_METHODS,
        'can_disable': False,
        'active': True,
        'module': SKILL_TOOL_CONFIG.module,
    })
    return result


def filter_tools(
    configs: list[ToolConfig],
    available_tools: list[str] | None = None,
) -> list[ToolConfig]:
    result = []
    for cfg in configs:
        if available_tools is not None and cfg.name not in available_tools:
            continue
        if not tool_is_active(cfg):
            continue
        result.append(cfg)
    return result


def collect_system_prompt_appendices(
    configs: list[ToolConfig],
    extra_appendices: tuple[SystemPromptAppendix, ...] = (),
) -> dict[str, list[str]]:
    """Collect active tool prompt appendices with stable per-section deduplication."""
    collected: dict[str, list[str]] = {}
    seen: dict[str, set[str]] = {}
    appendices = [cfg.appendix_system_prompt for cfg in configs if cfg.appendix_system_prompt]
    appendices.extend(extra_appendices)
    for appendix in appendices:
        for section, values in appendix.items():
            entries = (values,) if isinstance(values, str) else values
            for content in entries:
                original = content.strip()
                if not original:
                    continue
                dedupe_key = ' '.join(original.split())
                section_seen = seen.setdefault(section, set())
                if dedupe_key in section_seen:
                    continue
                section_seen.add(dedupe_key)
                collected.setdefault(section, []).append(original)
    return collected

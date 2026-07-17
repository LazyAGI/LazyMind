# flake8: noqa: Q000
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
from lazymind.chat.engine.tools.ask_user import ask_user
from lazymind.chat.engine.subagent.tools import find_user_attachment, read_user_attachment

SystemPromptAppendix = dict[str, str | tuple[str, ...]]
SYSTEM_PROMPT_APPENDIX_SECTIONS = ('tool_policy', 'safety', 'output_contract', 'response_policy')

IMAGE_MARKDOWN_OUTPUT_APPENDIX: SystemPromptAppendix = {
    'output_contract': (
        '# Image path formatting (mandatory)\n'
        'When showing images in your answer, you MUST copy the `image_markdown` field from '
        'tool results verbatim when it is available. \n'
        'If `image_markdown` is absent, copy the `image_url` or signed `text` field that '
        'starts with `/static-files/` exactly.\n'
        'Rules:\n'
        '- Use Markdown image syntax only: `![alt](/static-files/...?expires=...&sig=...)`.\n'
        '- NEVER invent hosts or prefixes (`https://ext.lazymind.ai`, `agent-cdn.minimax.io`, '
        'OCR ports, CDN tool_output URLs, etc.).\n'
        '- NEVER rewrite `/static-files/` paths into `http://` or `https://` URLs.\n'
        '- Do not use MiniMax/agent CDN links for local images; they are invalid for this UI.\n'
        '- Do not paste bare filesystem paths (`/var/lib/lazymind/uploads/...`) in answers.',
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
        '# Knowledge evidence citation rules\n'
        'When answering with evidence retrieved from a knowledge base or uploaded '
        'document index, cite using the original `[[document.chunk]]` markers present '
        'in the retrieved evidence. Do not invent, rewrite, or fabricate citation markers.',
    ),
}
ATTACHED_FILES_TOOL_POLICY_APPENDIX: SystemPromptAppendix = {
    'tool_policy': (
        '# Attached file rules\n'
        'Attachments are listed for reference only — do NOT parse or read them automatically.\n'
        '- `find_user_attachment(filename, turn=N)`: get path/url to pass to image tools, plugins, '
        '`vision_extractor`, or `save_plugin_artifact`. Prefer this for images when the task is '
        'visual (edit, generate, plugin) or you only need the file location.\n'
        '- `read_user_attachment(filename, turn=N)`: extract TEXT — OCR for pdf/doc/docx/pptx, or a '
        'text description via vision for images. Use only when you need document text or a textual '
        'answer about image content (e.g. "what does this document say", "describe this diagram").\n'
        'Supported uploads: png, jpg, jpeg, pdf, doc, docx, pptx.\n'
        '- Default to the current turn (marked 当前轮次) when the user says '
        '"this image / 这张图 / 这个文件" without naming a turn.\n'
        '- For knowledge-base questions about indexed documents, you may also use '
        '`kb_tmp_search` or other `kb_*` tools when appropriate.',
    ),
}
ASK_USER_TOOL_POLICY_APPENDIX: SystemPromptAppendix = {
    'tool_policy': (
        '# User clarification and confirmation rules (mandatory)\n'
        'Whenever you need the user to answer a question before you can continue—including '
        'clarification, confirmation, approval, choosing among options, or supplying missing '
        'information—you MUST call `ask_user`. Never ask a question that requires a user response '
        'as plain assistant text, in a status update, or in the final answer. If you can proceed '
        'safely with a reasonable assumption and do not actually need a response, do not ask. '
        'Treat all of these as requiring `ask_user`: asking the user to choose A or B; asking what '
        'they want to do next; collecting goals, preferences, constraints, or missing details; '
        'requesting confirmation, approval, or permission; giving a quiz, exercise, interview, or '
        'knowledge check; and ending with an invitation that expects a reply. Examples include '
        '"Do you want the answer now or time to think?", "Are you asking for A or B?", '
        '"Which option should we use?", "Would you like me to continue?", and "Please tell me your '
        'specific intent." A question mark is not required: imperatives such as "Choose one", '
        '"Tell me your preference", and "Confirm before I continue" also require `ask_user`. '
        'Rhetorical questions that expect no answer do not require it. '
        'Calling `ask_user` ends the current turn; continue only after the user answers.',
    ),
}
KNOWLEDGE_SEARCH_TOOL_POLICY_APPENDIX: SystemPromptAppendix = {
    'tool_policy': (
        "# Search Tool Rules (CRITICAL — follow strictly)\n"
        "If only the `KBToolkit` gateway is available, you MUST activate it first by calling "
        "its activation tool (e.g. `get_KBToolkit_methods`). If concrete methods such as "
        "`KBToolkit_kb_search` are already available, call the appropriate search method directly. "
        "A selected knowledge base is an explicit user instruction to search it, not merely permission "
        "to search it. The presence of concrete `KBToolkit_kb_search` or "
        "`KBToolkit_kb_keyword_search` methods means a knowledge base is selected. In that case, "
        "your first substantive action for the turn MUST be one of those searches. Do not answer "
        "from memory, announce that you could search later, ask whether you should search, or start "
        "a plugin before searching. Use the knowledge-base search method FIRST for every retrieval "
        "need — no exceptions. Do not skip it because you think the web might have "
        "better information, or because the topic seems general, popular, or common "
        "knowledge. The knowledge base is your primary evidence source.\n\n"
        "Only after the knowledge-base search returns zero results or explicitly irrelevant results "
        "may you fall back to provider-specific search tools. "
        "You MUST NOT use any non-knowledge-base retrieval tool before trying knowledge-base tools.\n\n"
        "**Keyword search vs semantic search — which one to use:**\n"
        "When the user mentions a specific document name (e.g., 'xxx.pdf', 'report.docx', "
        "'slides.pptx') and asks about particular terms, phrases, or content within that "
        "document, prefer `kb_keyword_search` with `target=<document name>`, "
        "`target_type='file_name'`, and `keyword=<specific terms>`. This is faster and more precise "
        "for document-scoped exact matching.\n"
        "For `keyword`, extract the core term(s) the user is asking about (e.g., a single "
        "word or short phrase like 'file1' or 'Redis timeout'), not the entire query "
        "sentence. If the first attempt returns zero results, try a shorter or alternative "
        "keyword before considering fallback.\n"
        "When the keyword search returns results, answer directly from them — do not "
        "follow up with semantic search unless the returned content is clearly irrelevant "
        "or empty.\n"
        "Use semantic search only for open-ended queries where no specific document "
        "is named. If keyword search returns zero results after trying alternative "
        "keywords, fall back to semantic search.\n\n"
        "When the user gives a concrete URL or asks you to inspect a specific page, "
        "still try the knowledge-base search first; use `url_fetch` only when the knowledge base has "
        "no relevant result.\n\n"
        "For papers, research topics, arXiv ids, abstracts, or author-related questions, "
        "still try the knowledge-base search first; after knowledge-base evidence is unavailable or "
        "insufficient, prefer `AcademicSearchToolkit` over general web search tools. "
        "When answering with knowledge-base evidence, cite with the original `[[document.chunk]]` "
        "markers. When answering with web search tools, `url_fetch`, "
        "or `AcademicSearchToolkit`, do not "
        "fabricate `[[document.chunk]]`; instead, mention the source title or URL plainly.\n"
    ),
}
WEB_SEARCH_TOOL_POLICY_APPENDIX: SystemPromptAppendix = {
    'tool_policy': (
        '# Web Search Tool Rules\n'
        'When using `web_search`, the `query` must represent one search intent. '
        'If the user asks to search multiple unrelated keywords or topics, call '
        '`web_search` separately for each keyword/topic. Do not combine unrelated '
        'terms into one `query` with spaces, commas, punctuation, or list-like text.',
    ),
}
CLOUD_DOCUMENT_TOOL_POLICY_APPENDIX: SystemPromptAppendix = {
    'tool_policy': (
        '# Cloud document link rules\n'
        'When the user provides a Feishu/Lark document URL, use the Feishu file-system tools '
        'to resolve the link and read the document before summarizing or analyzing it.\n'
        'When the user provides a Notion URL (`notion.so`, `notion.site`, `notion.com`, or '
        '`app.notion.com`), use the Notion file-system tools first. Prefer resolving the '
        'link, then reading with references when the task asks for analysis, summary, or '
        'linked-page context. Do not fall back to generic URL fetching for private Notion '
        'pages unless Notion tools are unavailable or unauthorized.',
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
    capability_id: str = ''
    equivalence_scope: str = 'infrastructure'
    provider_id: str = ''
    product_id: str = ''
    input_schema: dict[str, Any] | None = None
    output_schema: dict[str, Any] | None = None
    required_config: list[str] | None = None
    appendix_system_prompt: SystemPromptAppendix | None = None

    def __post_init__(self) -> None:
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

ASK_USER_TOOL_CONFIG = ToolConfig(
    name='ask_user',
    label='向用户提问',
    description='通过结构化交互卡片向用户澄清或确认信息',
    tool=ask_user,
    module='interaction',
    appendix_system_prompt=ASK_USER_TOOL_POLICY_APPENDIX,
)

USER_ATTACHMENT_TOOL_CONFIGS = (
    ToolConfig(
        name='read_user_attachment',
        label='读取用户附件',
        description='按需提取用户附件内容',
        tool=read_user_attachment,
        module='attachment',
        appendix_system_prompt=ATTACHED_FILES_TOOL_POLICY_APPENDIX,
    ),
    ToolConfig(
        name='find_user_attachment',
        label='查找用户附件',
        description='查找用户附件路径而不解析内容',
        tool=find_user_attachment,
        module='attachment',
        appendix_system_prompt={
            'tool_policy': ATTACHED_FILES_TOOL_POLICY_APPENDIX['tool_policy'],
            'output_contract': IMAGE_MARKDOWN_OUTPUT_APPENDIX['output_contract'],
        },
    ),
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
            'tool_policy': KNOWLEDGE_SEARCH_TOOL_POLICY_APPENDIX['tool_policy'],
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
        appendix_system_prompt={
            'tool_policy': KNOWLEDGE_SEARCH_TOOL_POLICY_APPENDIX['tool_policy'],
            'output_contract': KNOWLEDGE_CITATION_OUTPUT_APPENDIX['output_contract'],
        },
    ),
    ToolConfig(
        name='data_sources', label='数据源查询',
        description='仅查询已配置的数据源提供方；不用于查询可用工具或通用能力',
        tool=list_data_sources, module='data', label_en='Data Sources',
        description_en=(
            'List configured data-source providers only; not a catalog of '
            'available tools or general capabilities.'
        ),
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
        capability_id='web_search',
        equivalence_scope='provider_bound',
        input_schema={'query': 'string'}, output_schema={'results': 'list'}, required_config=['search_provider'],
        appendix_system_prompt=WEB_SEARCH_TOOL_POLICY_APPENDIX,
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
        appendix_system_prompt=CLOUD_DOCUMENT_TOOL_POLICY_APPENDIX,
    ),
    ToolConfig(
        name='schedule', label='定时任务', description='创建、查询、修改、取消和立即触发定时任务',
        tool=build_schedule_toolkit(), module='execution', label_en='Schedules',
        description_en='Create, inspect, update, cancel, and trigger recurring schedules.',
    ),
]


def _tool_summary(tool: Any) -> str:
    try:
        doc = inspect.getdoc(tool)
        return docstring_parser.parse(doc).short_description if doc else ''
    except Exception:
        return ''


def _extract_methods(instance: Any) -> list[dict]:
    if isinstance(instance, dict):
        return _extract_group_methods(instance.get('tools', []))
    public_apis = getattr(instance, '__public_apis__', None)
    if public_apis is not None:
        methods = []
        for method_name in public_apis:
            resolved_name = instance.__class__.__name__ if method_name == '__call__' else method_name
            method = getattr(instance, method_name, None)
            methods.append({'name': resolved_name, 'summary': _tool_summary(method) if method else ''})
        return methods

    if callable(instance):
        name = getattr(instance, '__name__', '')
        return [{'name': name, 'summary': _tool_summary(instance)}]

    return []


def _extract_group_methods(instances: list) -> list[dict]:
    methods = []
    for inst in instances:
        methods.append({
            'name': inst.__class__.__name__,
            'summary': _tool_summary(inst),
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
    if isinstance(tool, tuple) and len(tool) == 2:
        return tool[0]
    return tool


def _registration_key_source(tool: Any) -> Callable[[], Any] | None:
    if isinstance(tool, tuple) and len(tool) == 2 and callable(tool[1]):
        return tool[1]
    return None


def tool_is_active(cfg: ToolConfig) -> bool:
    if cfg.model_role and not is_model_role_available(cfg.model_role):
        return False
    key_source = _registration_key_source(cfg.tool)
    if key_source and not _key_source_is_active(key_source):
        return False
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
        result.append({
            'name': cfg.name,
            'label': cfg.label_en or cfg.label if use_english else cfg.label,
            'description': cfg.description_en or cfg.description if use_english else cfg.description,
            'methods': _extract_methods(_registration_target(cfg.tool)),
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

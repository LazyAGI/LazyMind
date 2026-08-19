from __future__ import annotations

import os
import time
from contextvars import ContextVar

import pytest

from lazymind.chat.engine.skills import SkillRetriever
from lazymind.chat.engine.tools.skill_search import build_search_skills_tool


CATALOG = [
    {
        'id': 'resume-assistant',
        'name': 'Resume Assistant',
        'description': 'Turn scattered experience into a professional resume or CV.',
        'aliases': ['简历', '履历'],
        'tags': ['career'],
    },
    {
        'id': 'hot-news-summary',
        'name': 'Hot News Summary',
        'description': '抓取并整理今日热点、热门话题和新闻热搜。',
        'aliases': ['每日热点'],
        'tags': ['news'],
    },
    {
        'id': 'spreadsheet-cleaner',
        'name': 'Spreadsheet Cleaner',
        'description': 'Normalize and clean spreadsheet rows.',
        'tags': ['data'],
    },
]


def _descriptor(skill_id: str, description: str, *aliases: str) -> dict:
    return {
        'id': skill_id,
        'name': skill_id.replace('-', ' ').title(),
        'description': description,
        'aliases': list(aliases),
    }


LARGE_CATALOG = [
    _descriptor(
        'resume-assistant',
        'Turn scattered work experience into a professional resume, CV, curriculum vitae, 求职材料或个人履历。',
        '简历', '履历',
    ),
    _descriptor(
        'hot-news-summary',
        "Fetch and summarize today's trending news, breaking stories, 今日热点、新闻热搜和热门话题。",
        '每日热点', '今日新闻',
    ),
    _descriptor('academic-writer', '撰写学术论文、摘要、研究方法和实验结论。'),
    _descriptor('arxiv-search', 'Search arXiv preprints and retrieve papers by topic or author.'),
    _descriptor('bibliography-manager', '整理参考文献、BibTeX 和论文书目。'),
    _descriptor('spreadsheet-cleaner', '清洗 Excel 或 CSV，处理缺失值、重复行和格式错误。'),
    _descriptor('csv-profiler', 'Profile CSV columns, distributions, types, and anomalies.'),
    _descriptor('sql-analyst', '编写 SQL 查询并分析数据库表、指标和聚合结果。'),
    _descriptor('market-research', '研究市场规模、行业趋势、客户与商业机会。'),
    _descriptor('competitor-analysis', '分析竞品功能、定价、定位、优缺点和竞争格局。'),
    _descriptor('prd-writer', '生成产品需求文档 PRD、用户故事和验收标准。'),
    _descriptor('meeting-minutes', '把会议讨论整理成会议纪要、决策和待办事项。'),
    _descriptor('ppt-generator', '根据提纲和材料生成 PowerPoint 演示文稿。'),
    _descriptor('pdf-exporter', '把文档或简历导出为排版稳定的 PDF 文件。'),
    _descriptor('diagram-maker', '绘制流程图、架构图、时序图和 Mermaid 图表。'),
    _descriptor('image-generator', '根据文字描述生成创意图片、插画和海报。'),
    _descriptor('image-editor', '编辑已有图片，移除元素、标注亮点或修改风格。'),
    _descriptor('translator', '在中文、英文及其他语言之间翻译文本。'),
    _descriptor('code-reviewer', '审查代码缺陷、可维护性、安全性和测试覆盖。'),
    _descriptor('python-debugger', '诊断 Python 异常、脚本错误和性能问题。'),
    _descriptor('api-docs', '生成和维护 API 文档、参数说明与调用示例。'),
    _descriptor('legal-contract-review', '审阅合同条款、法律风险与权利义务。'),
    _descriptor('travel-planner', '规划旅行路线、住宿、交通和景点日程。'),
    _descriptor('weather-forecast', '查询天气预报、温度、降雨和空气质量。'),
    _descriptor('email-drafter', '起草、润色和回复工作邮件。'),
    _descriptor('social-copywriter', '创作社交媒体文案、标题和营销帖子。'),
    _descriptor('interview-coach', '准备面试问题、模拟问答和反馈。'),
    _descriptor('job-search', '搜索职位、筛选招聘信息并规划求职。'),
    _descriptor('cover-letter', '撰写求职信和岗位申请信。'),
    _descriptor('personal-bio', '撰写个人简介、自我介绍和作者介绍。'),
    _descriptor('knowledge-base-qa', '基于内部知识库文档回答问题并引用来源。'),
    _descriptor('web-research', '在网页上检索资料、核验来源并汇总结论。'),
    _descriptor('literature-review', '综述多篇论文的研究脉络、方法与不足。'),
    _descriptor('data-visualization', '把数据制作成图表、仪表盘和可视化报告。'),
    _descriptor('finance-model', '建立财务预测、现金流、预算和估值模型。'),
    _descriptor('stock-monitor', '监控股票行情、公告、价格异动和投资组合。'),
    _descriptor('product-roadmap', '制定产品路线图、版本目标和里程碑。'),
    _descriptor('user-research', '设计用户访谈、可用性测试并提炼需求。'),
    _descriptor('survey-analysis', '分析问卷结果、交叉统计和开放题反馈。'),
    _descriptor('sentiment-analysis', '识别评论或舆情中的情绪与观点倾向。'),
    _descriptor('calendar-planner', '安排日历、会议时间和个人日程。'),
    _descriptor('task-manager', '拆解任务、跟踪进度、优先级和负责人。'),
    _descriptor('invoice-parser', '从发票中提取金额、税号、日期和开票方。'),
    _descriptor('receipt-ocr', 'OCR 识别小票图片中的商品、金额和时间。'),
    _descriptor('audio-transcriber', '把访谈、会议或录音转写成文字。'),
    _descriptor('video-summary', '总结视频内容、时间点和主要观点。'),
    _descriptor('citation-checker', '检查论文引用、引文一致性和来源真实性。'),
    _descriptor('grammar-editor', '校对语法、拼写、表达和文章风格。'),
    _descriptor('presentation-design', '优化演示稿版式、视觉层级和配色。'),
    _descriptor(
        'computer-vision-explainer',
        'Explain CV as computer vision, image recognition, detection, and visual models.',
        'CV',
    ),
]


LEXICAL_RECALL_CASES = [
    ('把这些零散经历整理成一份专业求职履历', 'resume-assistant'),
    ('帮我优化 CV 简历', 'resume-assistant'),
    ('polish my curriculum vitae for a job application', 'resume-assistant'),
    ('今天有什么新闻热搜', 'hot-news-summary'),
    ("what's trending in the news today", 'hot-news-summary'),
    ('把这个 CSV 的缺失值和重复行清理掉', 'spreadsheet-cleaner'),
    ('分析竞品的功能和定价', 'competitor-analysis'),
    ('把访谈录音转成文字', 'audio-transcriber'),
    ('检查论文引用是否真实一致', 'citation-checker'),
    ('把会议讨论整理成纪要和待办', 'meeting-minutes'),
    ('从一堆发票里提取金额和税号', 'invoice-parser'),
    ('生成一个包含用户故事和验收标准的 PRD', 'prd-writer'),
    ('帮我审查这份合同的法律风险', 'legal-contract-review'),
    ('把销售数据做成图表和仪表盘', 'data-visualization'),
    ('分析问卷里的开放题反馈', 'survey-analysis'),
    ('CV 在 computer vision 里是什么意思', 'computer-vision-explainer'),
]

SEMANTIC_RECALL_CASES = [
    ('把过往职业背景变成一份应聘档案', 'resume-assistant'),
    ('给我一份今天外界发生了什么的速览', 'hot-news-summary'),
    ('这份表里脏数据太多，帮我收拾一下', 'spreadsheet-cleaner'),
    ('我想知道同类产品都是怎么卖的', 'competitor-analysis'),
    ('把这段访谈变成可读文本', 'audio-transcriber'),
    ('这些出处靠不靠谱', 'citation-checker'),
    ('这里的 CV 指图像识别方向', 'computer-vision-explainer'),
]

_CONCEPT_SKILLS = {
    'resume-assistant': 0,
    'hot-news-summary': 1,
    'spreadsheet-cleaner': 2,
    'competitor-analysis': 3,
    'audio-transcriber': 4,
    'citation-checker': 5,
    'computer-vision-explainer': 6,
}
_CONCEPT_QUERIES = {query: _CONCEPT_SKILLS[skill_id] for query, skill_id in SEMANTIC_RECALL_CASES}


def _fixture_semantic_embedder(texts: list[str]) -> list[list[float]]:
    vectors = []
    for text in texts:
        concept = _CONCEPT_QUERIES.get(text)
        if concept is None:
            concept = _CONCEPT_SKILLS.get(text.split('\n', 1)[0])
        vector = [0.0] * 8
        if concept is None:
            vector[7] = 1.0
        else:
            vector[concept] = 1.0
        vectors.append(vector)
    return vectors


def test_small_catalog_exposes_all_metadata_without_embedding() -> None:
    retriever = SkillRetriever(
        embedder=lambda _: (_ for _ in ()).throw(AssertionError('embedding must not run')),
        small_catalog_threshold=3,
    )

    result = retriever.retrieve('帮我整理简历', CATALOG, limit=1)

    assert result.strategy == 'all'
    assert result.skill_ids == [item['id'] for item in CATALOG]


def test_catalog_threshold_switches_from_full_metadata_to_hybrid_top_k() -> None:
    retriever = SkillRetriever(
        embedder=_fixture_semantic_embedder,
        cache_namespace='catalog-threshold',
        small_catalog_threshold=20,
    )

    small_result = retriever.retrieve(
        '把过往职业背景变成一份应聘档案',
        LARGE_CATALOG[:20],
        limit=3,
    )
    large_result = retriever.retrieve(
        '把过往职业背景变成一份应聘档案',
        LARGE_CATALOG[:21],
        limit=3,
    )

    assert small_result.strategy == 'all'
    assert len(small_result.hits) == 20
    assert large_result.strategy == 'hybrid'
    assert len(large_result.hits) == 3
    assert 'resume-assistant' in large_result.skill_ids


def test_lexical_fallback_recalls_natural_news_wording() -> None:
    retriever = SkillRetriever(embedder=None, small_catalog_threshold=0)

    result = retriever.retrieve('用投资视角看看今天的新闻热搜', CATALOG, limit=2)

    assert result.strategy == 'lexical'
    assert result.skill_ids[0] == 'hot-news-summary'
    assert result.embedding_error == 'embedding model unavailable'


def test_lexical_fallback_does_not_pad_results_with_zero_score_decoys() -> None:
    catalog = [
        _descriptor('resume-assistant', 'Build a professional curriculum vitae.'),
        _descriptor('weather-forecast', 'Forecast temperature and rain.'),
        _descriptor('invoice-parser', 'Extract totals and tax numbers from invoices.'),
        _descriptor('diagram-maker', 'Draw architecture diagrams.'),
    ]
    retriever = SkillRetriever(embedder=None, small_catalog_threshold=0)

    result = retriever.retrieve('curriculum vitae', catalog, limit=3)

    assert result.skill_ids == ['resume-assistant']
    assert result.hits[0].score > 0


def test_large_catalog_lexical_fallback_meets_recall_at_three_target() -> None:
    assert len(LARGE_CATALOG) == 50
    retriever = SkillRetriever(embedder=None, small_catalog_threshold=20)
    misses = []

    for index, (query, expected_skill) in enumerate(LEXICAL_RECALL_CASES):
        catalog = LARGE_CATALOG if index % 2 == 0 else list(reversed(LARGE_CATALOG))
        result = retriever.retrieve(query, catalog, limit=3)
        if expected_skill not in result.skill_ids:
            misses.append((query, expected_skill, result.skill_ids))

    assert len(misses) <= 1, f'Recall@3 below 15/16: {misses}'


def test_large_catalog_compound_request_recalls_both_workflows() -> None:
    retriever = SkillRetriever(embedder=None, small_catalog_threshold=20)

    result = retriever.retrieve(
        '先汇总今天的新闻热搜，再把我的零散经历整理成 CV 简历',
        list(reversed(LARGE_CATALOG)),
        limit=5,
    )

    assert {'hot-news-summary', 'resume-assistant'} <= set(result.skill_ids)


def test_dense_and_lexical_rankings_are_fused() -> None:
    def embed(texts: list[str]) -> list[list[float]]:
        vectors = []
        for text in texts:
            lowered = text.lower()
            if '求职材料' in text or 'resume' in lowered or 'cv' in lowered:
                vectors.append([1.0, 0.0])
            elif '新闻' in text or 'news' in lowered:
                vectors.append([0.0, 1.0])
            else:
                vectors.append([0.2, 0.2])
        return vectors

    retriever = SkillRetriever(
        embedder=embed,
        cache_namespace='dense-test',
        small_catalog_threshold=0,
    )

    result = retriever.retrieve('把零散经历变成求职材料', CATALOG, limit=2)

    assert result.strategy == 'hybrid'
    assert result.skill_ids[0] == 'resume-assistant'
    assert 'dense' in result.hits[0].channels


def test_large_catalog_hybrid_retrieval_recalls_semantic_paraphrases() -> None:
    retriever = SkillRetriever(
        embedder=_fixture_semantic_embedder,
        cache_namespace='large-semantic-matrix',
        small_catalog_threshold=20,
    )
    misses = []

    for catalog_order, catalog in (
        ('forward', LARGE_CATALOG),
        ('reversed', list(reversed(LARGE_CATALOG))),
    ):
        for query, expected_skill in SEMANTIC_RECALL_CASES:
            result = retriever.retrieve(query, catalog, limit=3)
            if expected_skill not in result.skill_ids:
                misses.append((catalog_order, query, expected_skill, result.skill_ids))
                continue
            expected_hit = next(hit for hit in result.hits if hit.descriptor.skill_id == expected_skill)
            assert 'dense' in expected_hit.channels
            assert result.strategy == 'hybrid'
            assert len(result.skill_ids) == len(set(result.skill_ids)) == 3

    assert misses == []


def test_hybrid_retrieval_disambiguates_cv_with_semantics_and_keywords() -> None:
    query = '这里的 CV 指图像识别方向'
    retriever = SkillRetriever(
        embedder=_fixture_semantic_embedder,
        cache_namespace='cv-disambiguation',
        small_catalog_threshold=20,
    )

    result = retriever.retrieve(query, LARGE_CATALOG, limit=3)

    assert result.skill_ids[0] == 'computer-vision-explainer'
    assert result.hits[0].channels == ('lexical', 'dense')


def test_fusion_keeps_a_strong_dense_leader_amid_lexical_collisions() -> None:
    catalog = [
        _descriptor('records-organizer', '整理职业背景、应聘档案和历史材料。'),
        _descriptor('career-planner', '规划职业背景和长期发展方向。'),
        _descriptor('application-tracker', '跟踪应聘档案和申请状态。'),
        _descriptor('resume-assistant', 'Create a polished resume or CV from work experience.'),
    ]
    query = '把过往职业背景变成一份应聘档案'

    def embed(texts: list[str]) -> list[list[float]]:
        vectors = []
        for text in texts:
            skill_id = text.split('\n', 1)[0]
            if text == query or skill_id == 'resume-assistant':
                vectors.append([1.0, 0.0])
            else:
                vectors.append([0.1, 0.995])
        return vectors

    retriever = SkillRetriever(
        embedder=embed,
        cache_namespace='dense-leader-coverage',
        small_catalog_threshold=0,
    )

    result = retriever.retrieve(query, catalog, limit=3)

    assert 'resume-assistant' in result.skill_ids
    resume_hit = next(hit for hit in result.hits if hit.descriptor.skill_id == 'resume-assistant')
    assert resume_hit.channels == ('dense',)


def test_embedding_failure_falls_back_without_dropping_candidates() -> None:
    def broken_embed(_: list[str]) -> list[list[float]]:
        raise RuntimeError('provider unavailable')

    retriever = SkillRetriever(
        embedder=broken_embed,
        cache_namespace='failure-test',
        small_catalog_threshold=0,
    )

    result = retriever.retrieve('今天有什么热点', CATALOG, limit=2)

    assert result.strategy == 'lexical'
    assert result.skill_ids[0] == 'hot-news-summary'
    assert 'provider unavailable' in result.embedding_error


def test_embedding_timeout_falls_back_to_lexical_results() -> None:
    def slow_embed(texts: list[str]) -> list[list[float]]:
        time.sleep(0.25)
        return [[1.0, 0.0] for _ in texts]

    retriever = SkillRetriever(
        embedder=slow_embed,
        cache_namespace='timeout-test',
        small_catalog_threshold=0,
        embedding_timeout_seconds=0.01,
    )

    result = retriever.retrieve('今天有什么新闻热搜', LARGE_CATALOG, limit=3)

    assert result.strategy == 'lexical'
    assert result.skill_ids[0] == 'hot-news-summary'
    assert 'embedding exceeded' in result.embedding_error


def test_embedding_worker_inherits_request_context() -> None:
    request_model = ContextVar('request_model', default='missing')
    request_model.set('configured-embed-model')

    def contextual_embed(texts: list[str]) -> list[list[float]]:
        if request_model.get() != 'configured-embed-model':
            raise RuntimeError('request model context was lost')
        return [[1.0, float(index)] for index, _ in enumerate(texts)]

    retriever = SkillRetriever(
        embedder=contextual_embed,
        cache_namespace='context-test',
        small_catalog_threshold=0,
    )

    result = retriever.retrieve('整理求职材料', CATALOG, limit=2)

    assert result.strategy == 'hybrid'
    assert result.embedding_error == ''


def test_catalog_embeddings_are_batched_reused_and_selectively_refreshed() -> None:
    batches = []

    def recording_embedder(texts: list[str]) -> list[list[float]]:
        batches.append(list(texts))
        return _fixture_semantic_embedder(texts)

    catalog = [dict(item) for item in LARGE_CATALOG]
    retriever = SkillRetriever(
        embedder=recording_embedder,
        cache_namespace='batch-and-refresh-contract',
        small_catalog_threshold=20,
    )

    retriever.retrieve(SEMANTIC_RECALL_CASES[0][0], catalog, limit=5)
    retriever.retrieve(SEMANTIC_RECALL_CASES[1][0], catalog, limit=5)
    changed_catalog = [dict(item) for item in catalog]
    changed_catalog[10]['description'] += ' Updated metadata.'
    retriever.retrieve(SEMANTIC_RECALL_CASES[2][0], changed_catalog, limit=5)

    assert [len(batch) for batch in batches] == [51, 1, 2]


def test_search_tool_exposes_only_retrieved_allowed_skills() -> None:
    class Manager:
        def __init__(self) -> None:
            self.exposed = []

        def list_skill_metadata(self, scope: str):
            assert scope == 'allowed'
            return CATALOG

        def expose_skills(self, names):
            self.exposed.extend(names)
            by_id = {item['id']: item for item in CATALOG}
            return {
                'status': 'ok',
                'skills': [by_id[name] for name in names if name in by_id],
                'errors': [],
            }

    manager = Manager()
    tool = build_search_skills_tool(
        manager,
        SkillRetriever(embedder=None, small_catalog_threshold=0),
    )

    result = tool('我需要整理 CV', limit=1)

    assert result['skills'][0]['id'] == 'resume-assistant'
    assert manager.exposed == ['resume-assistant']


@pytest.mark.skipif(
    os.environ.get('RUN_LIVE_SKILL_RETRIEVAL') != '1'
    or not os.environ.get('LIVE_SKILL_EMBED_API_KEY'),
    reason='requires explicit live retrieval opt-in and an embedding API key',
)
def test_live_embedding_meets_large_catalog_semantic_recall_target() -> None:
    import lazyllm

    source = os.environ.get('LIVE_SKILL_EMBED_SOURCE', 'siliconflow')
    model = os.environ.get('LIVE_SKILL_EMBED_MODEL', 'BAAI/bge-m3')
    embedder = lazyllm.OnlineEmbeddingModule(
        source=source,
        model=model,
        type='embed',
        api_key=os.environ['LIVE_SKILL_EMBED_API_KEY'],
        timeout=15,
    )
    retriever = SkillRetriever(
        embedder=embedder,
        cache_namespace=f'{source}-{model}-live-eval',
        small_catalog_threshold=20,
        embedding_timeout_seconds=3.0,
    )
    misses = []
    provider_errors = []

    for query, expected_skill in SEMANTIC_RECALL_CASES:
        result = retriever.retrieve(query, LARGE_CATALOG, limit=5)
        if result.embedding_error:
            provider_errors.append({
                'query': query,
                'error': result.embedding_error,
            })
        if expected_skill not in result.skill_ids:
            misses.append({
                'query': query,
                'expected': expected_skill,
                'actual': result.skill_ids,
                'strategy': result.strategy,
                'embedding_error': result.embedding_error,
            })

    assert not provider_errors, f'live embedding provider unavailable: {provider_errors}'
    assert len(misses) <= 1, f'live Recall@5 below 6/7: {misses}'

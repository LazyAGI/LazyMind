from __future__ import annotations

import copy
import json
import threading

import lazyllm
from lazyllm.common import new_session
from lazyllm.tools import ReactAgent
from lazyllm.tools.agent.skill_manager import SkillManager

from lazymind.chat.engine.skills import SkillRetriever
from lazymind.chat.engine.tools.skill_search import build_search_skills_tool


def _write_skill(root, skill_id: str, description: str, aliases=()) -> None:
    directory = root / skill_id
    directory.mkdir()
    alias_yaml = '[' + ', '.join(aliases) + ']'
    (directory / 'SKILL.md').write_text(
        '---\n'
        f'name: {skill_id}\n'
        f'description: {description}\n'
        f'aliases: {alias_yaml}\n'
        '---\n'
        f'# {skill_id}\n',
        encoding='utf-8',
    )


class _DiscoveryLoopLLM:
    def __init__(self) -> None:
        self._module_id = f'discovery-loop-{id(self)}'
        self._state = {'round': 0}

    def share(self, **_kwargs):
        return copy.copy(self)

    def used_by(self, _module_id):
        return self

    def __call__(self, _input, **_kwargs):
        history = lazyllm.locals.get('chat_history', {}).get(self._module_id, [])
        current_messages = _input.get('input', []) if isinstance(_input, dict) else []
        observations = [*history, *current_messages]
        current_round = self._state['round']
        self._state['round'] += 1
        if current_round == 0:
            return {
                'role': 'assistant',
                'content': 'I need to discover a relevant workflow.',
                'tool_calls': [{
                    'id': 'search-1',
                    'type': 'function',
                    'function': {
                        'name': 'search_skills',
                        'arguments': json.dumps({'query': 'build a resume or CV', 'limit': 3}),
                    },
                }],
            }
        if current_round == 1:
            assert any(
                item.get('role') == 'tool' and 'resume-assistant' in str(item.get('content'))
                for item in observations
            ), 'the model did not receive search_skills results'
            return {
                'role': 'assistant',
                'content': 'The resume workflow is relevant; I will load it.',
                'tool_calls': [{
                    'id': 'get-1',
                    'type': 'function',
                    'function': {
                        'name': 'get_skill',
                        'arguments': json.dumps({'name': 'resume-assistant'}),
                    },
                }],
            }
        assert any(
            item.get('role') == 'tool' and 'RESUME_WORKFLOW_MARKER' in str(item.get('content'))
            for item in observations
        ), 'the model did not receive the loaded SKILL.md'
        return {'role': 'assistant', 'content': 'Loaded and used RESUME_WORKFLOW_MARKER.'}


class _RetryingDiscoveryLoopLLM:
    def __init__(self) -> None:
        self._module_id = f'retrying-discovery-loop-{id(self)}'
        self._state = {'round': 0}

    def share(self, **_kwargs):
        return copy.copy(self)

    def used_by(self, _module_id):
        return self

    def __call__(self, current_input, **_kwargs):
        current_messages = current_input.get('input', []) if isinstance(current_input, dict) else []
        observation = '\n'.join(str(item.get('content', '')) for item in current_messages)
        current_round = self._state['round']
        self._state['round'] += 1
        if current_round == 0:
            return _tool_call(
                'search-vague',
                'search_skills',
                {'query': '把过往背景变成应聘档案', 'limit': 3},
            )
        if current_round == 1:
            assert "'count': 0" in observation
            assert 'embedding model unavailable' in observation
            return _tool_call(
                'search-specific',
                'search_skills',
                {'query': 'professional resume CV', 'limit': 3},
            )
        if current_round == 2:
            assert 'resume-assistant' in observation
            return _tool_call('get-after-retry', 'get_skill', {'name': 'resume-assistant'})
        assert 'RESUME_WORKFLOW_MARKER' in observation
        return {'role': 'assistant', 'content': 'Recovered after refining the Skill search.'}


def _tool_call(call_id: str, name: str, arguments: dict) -> dict:
    return {
        'role': 'assistant',
        'content': '',
        'tool_calls': [{
            'id': call_id,
            'type': 'function',
            'function': {'name': name, 'arguments': json.dumps(arguments)},
        }],
    }


class _DirectGetSkillLLM:
    '''Fake LLM that calls get_skill immediately, without searching first.

    Mirrors the production flow where chat_service exposes the initial
    retrieval hits before the agent loop starts, so the loop's first action
    can already be get_skill rather than search_skills.
    '''

    def __init__(self, skill_name: str) -> None:
        self._module_id = f'direct-get-{id(self)}'
        self._skill_name = skill_name
        self._state = {'round': 0}

    def share(self, **_kwargs):
        return copy.copy(self)

    def used_by(self, _module_id):
        return self

    def __call__(self, _input, **_kwargs):
        history = lazyllm.locals.get('chat_history', {}).get(self._module_id, [])
        current_messages = _input.get('input', []) if isinstance(_input, dict) else []
        observations = [*history, *current_messages]
        current_round = self._state['round']
        self._state['round'] += 1
        if current_round == 0:
            return _tool_call('get-direct', 'get_skill', {'name': self._skill_name})
        assert any(
            item.get('role') == 'tool' and 'RESUME_WORKFLOW_MARKER' in str(item.get('content'))
            for item in observations
        ), 'the model did not receive the loaded SKILL.md via get_skill'
        return {'role': 'assistant', 'content': 'Loaded RESUME_WORKFLOW_MARKER.'}


def test_search_skills_expands_get_skill_visibility_in_same_agent_session(tmp_path) -> None:
    _write_skill(
        tmp_path,
        'resume-assistant',
        'Turn scattered experience into a professional resume or CV.',
        aliases=('简历', '履历'),
    )
    _write_skill(
        tmp_path,
        'hot-news-summary',
        '抓取并整理今日热点、热门话题和新闻热搜。',
        aliases=('每日热点',),
    )
    for index in range(23):
        _write_skill(tmp_path, f'filler-{index}', f'Unrelated capability number {index}.')

    allowed = ['resume-assistant', 'hot-news-summary', *(f'filler-{index}' for index in range(23))]
    manager = SkillManager(dir=str(tmp_path), skills=[], allowed_skills=allowed)
    search_skills = build_search_skills_tool(
        manager,
        SkillRetriever(embedder=None, small_catalog_threshold=20),
    )

    with new_session('skill-runtime-integration'):
        assert manager.get_skill('resume-assistant')['status'] == 'missing'

        result = search_skills('把这些零散经历整理成 CV', limit=3)

        assert result['skills'][0]['id'] == 'resume-assistant'
        assert manager.get_skill('resume-assistant')['status'] == 'ok'
        assert manager.get_skill('not-allowed')['status'] == 'missing'


def test_react_agent_can_discover_and_load_a_skill_after_the_first_round(tmp_path) -> None:
    _write_skill(
        tmp_path,
        'resume-assistant',
        'Turn scattered experience into a professional resume or CV. RESUME_WORKFLOW_MARKER',
        aliases=('简历', '履历'),
    )
    for index in range(49):
        _write_skill(tmp_path, f'decoy-{index}', f'Unrelated workflow number {index}.')

    allowed = ['resume-assistant', *(f'decoy-{index}' for index in range(49))]
    manager = SkillManager(dir=str(tmp_path), skills=[], allowed_skills=allowed)
    search_skills = build_search_skills_tool(
        manager,
        SkillRetriever(embedder=None, small_catalog_threshold=20),
    )
    agent = ReactAgent(
        llm=_DiscoveryLoopLLM(),
        tools=[search_skills],
        skill_manager=manager,
        max_retries=3,
        stream=False,
        enable_builtin_tools=False,
    )

    with new_session('skill-discovery-agent-loop'):
        assert manager.get_skill('resume-assistant')['status'] == 'missing'

        result = agent('Turn my scattered experience into a professional CV.')

        assert result == 'Loaded and used RESUME_WORKFLOW_MARKER.'
        assert manager.get_skill('resume-assistant')['status'] == 'missing'


def test_react_agent_can_refine_an_empty_skill_search_inside_the_loop(tmp_path) -> None:
    _write_skill(
        tmp_path,
        'resume-assistant',
        'Turn scattered experience into a professional resume or CV. RESUME_WORKFLOW_MARKER',
    )
    for index in range(49):
        _write_skill(tmp_path, f'decoy-{index}', f'Unrelated workflow number {index}.')

    allowed = ['resume-assistant', *(f'decoy-{index}' for index in range(49))]
    manager = SkillManager(dir=str(tmp_path), skills=[], allowed_skills=allowed)
    agent = ReactAgent(
        llm=_RetryingDiscoveryLoopLLM(),
        tools=[build_search_skills_tool(
            manager,
            SkillRetriever(embedder=None, small_catalog_threshold=20),
        )],
        skill_manager=manager,
        max_retries=4,
        stream=False,
        enable_builtin_tools=False,
    )

    with new_session('skill-search-refinement-agent-loop'):
        result = agent('把过往背景变成应聘档案。')

    assert result == 'Recovered after refining the Skill search.'


def test_initial_retrieval_exposure_is_visible_in_agent_loop(tmp_path) -> None:
    _write_skill(
        tmp_path,
        'resume-assistant',
        'Turn scattered experience into a professional resume or CV. RESUME_WORKFLOW_MARKER',
        aliases=('简历', '履历'),
    )
    for index in range(49):
        _write_skill(tmp_path, f'decoy-{index}', f'Unrelated workflow number {index}.')

    allowed = ['resume-assistant', *(f'decoy-{index}' for index in range(49))]
    manager = SkillManager(dir=str(tmp_path), skills=[], allowed_skills=allowed)
    agent = ReactAgent(
        llm=_DirectGetSkillLLM('resume-assistant'),
        tools=[],
        skill_manager=manager,
        max_retries=2,
        stream=False,
        enable_builtin_tools=False,
    )

    with new_session('initial-exposure-agent-loop'):
        assert manager.get_skill('resume-assistant')['status'] == 'missing'
        manager.expose_skills(['resume-assistant'])
        result = agent('Turn my scattered experience into a professional CV.')

    assert result == 'Loaded RESUME_WORKFLOW_MARKER.'


def test_concurrent_sessions_do_not_share_exposed_skills(tmp_path) -> None:
    _write_skill(tmp_path, 'resume-assistant', 'resume workflow', aliases=('简历',))
    _write_skill(tmp_path, 'hot-news-summary', 'news workflow', aliases=('热点',))
    manager = SkillManager(
        dir=str(tmp_path),
        skills=[],
        allowed_skills=['resume-assistant', 'hot-news-summary'],
    )

    results: dict[str, tuple[str, str]] = {}
    barrier = threading.Barrier(2)

    def run_resume_session() -> None:
        with new_session('concurrent-resume-session'):
            barrier.wait()
            manager.expose_skills(['resume-assistant'])
            results['resume'] = (
                manager.get_skill('resume-assistant')['status'],
                manager.get_skill('hot-news-summary')['status'],
            )

    def run_news_session() -> None:
        with new_session('concurrent-news-session'):
            barrier.wait()
            manager.expose_skills(['hot-news-summary'])
            results['news'] = (
                manager.get_skill('hot-news-summary')['status'],
                manager.get_skill('resume-assistant')['status'],
            )

    resume_thread = threading.Thread(target=run_resume_session)
    news_thread = threading.Thread(target=run_news_session)
    resume_thread.start()
    news_thread.start()
    resume_thread.join(timeout=30)
    news_thread.join(timeout=30)

    assert not resume_thread.is_alive()
    assert not news_thread.is_alive()
    assert results['resume'] == ('ok', 'missing')
    assert results['news'] == ('ok', 'missing')

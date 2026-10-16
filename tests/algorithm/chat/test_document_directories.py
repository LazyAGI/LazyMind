import asyncio
import json
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from lazyllm.tools.agent import ToolExecutionDisposition
from lazymind.directory.runtime import DirectoryRuntime


def record(name, result, args=None):
    return SimpleNamespace(tool_name=name, result=result, arguments=args or {}, validated_arguments=args or {},
                           call_id='call-1', disposition=ToolExecutionDisposition.EXECUTED, reason='')


def runtime():
    config = SimpleNamespace(
        names={'fs_google_search': 'googledrive', 'fs_google_read': 'googledrive',
               'fs_feishu_search': 'feishu', 'fs_feishu_read': 'feishu',
               'notion_search': 'mcp:msp_notion_connection1', 'notion_fetch': 'mcp:msp_notion_connection1'},
        services={'mcp:msp_notion_connection1': {
            'runtime': {'oauth': {'grant_id': 'grant1', 'grant_version': 1}},
        }},
        tool_config={'document_directory_scopes': {'googledrive': ['account-1'], 'feishu': ['tenant-user-1']}},
    )
    return DirectoryRuntime('user', 'conversation', 'history', 'run', config)


def test_retired_directory_reference_is_not_bound_to_model_resources():
    from lazymind.chat.engine.prompts.resource_usage import _normalize_explicit_resources
    resources = _normalize_explicit_resources({'mentions': [
        {'resource_type': 'document_directory', 'resource_ref': 'old-directory'},
        {'resource_type': 'skill', 'resource_ref': 'supported-skill'},
    ]})
    assert [(item.resource_type, item.resource_ref) for item in resources.mentions] == [
        ('skill', 'supported-skill'),
    ]


@pytest.mark.parametrize('tool,args,result,source,rid,read', [
    ('native_search', {'query': 'contract'}, {'status': 'ok', 'results': [{'path': '/tmp/contract.txt'}]},
     'local', '/tmp/contract.txt', 'unread'),
    ('read', {'path': '/tmp/contract.txt'},
     {'path': '/tmp/contract.txt', 'content': 'terms', 'offset': 1, 'end_line': 1, 'eof': True},
     'local', '/tmp/contract.txt', 'read'),
    ('read', {'path': '/home/u/.codex/sessions/a.jsonl'},
     {'content': '{"type":"response_item","payload":{"content":"terms"}}',
      'line_unit': 'physical', 'offset': 10, 'end_line': 20, 'eof': False},
     'agent_conversations', '/home/u/.codex/sessions/a.jsonl:physical:10:20', 'partial'),
    ('fs_google_search', {'keywords': 'contract'},
     [{'name': 'file-1', 'title': 'Contract', 'google_drive_path': 'googledrive:/file-1'}],
     'google_drive', 'file-1', 'unread'),
    ('fs_google_read', {'path': 'googledrive:/file-1'}, 'contract terms', 'google_drive', 'file-1', 'read'),
    ('fs_feishu_search', {'query': 'contract'},
     {'results': [{'result_meta': {'url': 'https://x.feishu.cn/docx/token1', 'token': 'token1', 'title': 'Contract'}}],
      'has_more': True}, 'feishu', 'token1', 'unread'),
    ('fs_feishu_read', {'path': 'https://x.feishu.cn/docx/token1'}, 'contract terms', 'feishu', 'token1', 'read'),
    ('notion_search', {'query': 'contract'}, {'content': [
        {'type': 'text', 'text': '{"results":[{"id":"3f4b8b30-a5b0-8119-bd8b-cf0b4999c02f","title":"Contract"}]}'},
    ]}, 'notion', '3f4b8b30a5b08119bd8bcf0b4999c02f', 'unread'),
    ('mail_search', {'query': 'contract'}, {'mailbox': 'a@example.com', 'items': [
        {'id': 'INBOX::42', 'subject': 'Contract', 'thread_id': '<abc@example.com>'},
    ]}, 'mail', '<abc@example.com>', 'unread'),
    ('mail_read', {'message_id': 'INBOX::42'},
     {'mailbox': 'a@example.com', 'id': 'INBOX::42', 'body': 'terms', 'thread_id': '<abc@example.com>'},
     'mail', '<abc@example.com>', 'read'),
])
def test_actual_source_envelopes(tool, args, result, source, rid, read):
    obs = runtime().normalize(record(tool, result, args))
    assert len(obs['candidates']) == 1
    item = obs['candidates'][0]
    assert (item['source'], item['resource_id'], item['read_state']) == (source, rid, read)
    assert item['namespace']
    if source == 'mail':
        assert item['locator']['thread_id'] == '<abc@example.com>'
        assert item['locator']['id'] == 'INBOX::42'


def test_scope_and_failure_are_not_reported_as_zero_or_complete():
    rt = runtime()
    denied = rt.normalize(record('fs_feishu_search', {'ok': False, 'value':
                          "400 Bad Request: {'code': 99991679, 'msg': 'Unauthorized.'}"}))
    assert denied['status'] == 'denied' and denied['incomplete'] and not denied['candidates']
    assert rt.normalize(record('native_search', {'status': 'unavailable', 'results': []}))['status'] == 'unavailable'
    assert rt.normalize(record('fs_google_search', []))['status'] == 'zero'
    assert rt.normalize(record('MailToolkit_search', {
        'status': 'partial', 'reason': 'search_incomplete', 'items': [],
    }))['incomplete']
    ambiguous = rt.configuration.tool_config['document_directory_scopes']['googledrive']
    ambiguous.append('account-2')
    value = rt.normalize(record('fs_google_search', [{'name': 'x', 'google_drive_path': 'googledrive:/x'}]))
    assert value['incomplete'] and not value['candidates']
    rejected = record('read', 'not executed', {'path': '/tmp/a'})
    rejected.disposition = ToolExecutionDisposition.SKIPPED
    value = rt.normalize(rejected)
    assert value['status'] == 'not_executed' and not value['candidates']
    assert rt.normalize(record('shell', 'opaque output', {'command': 'secret'}))['scope'] == {}
    assert rt.normalize(record('mail_send_draft', {'id': 'draft', 'mailbox': 'u'})) is None


def test_content_version_requires_source_metadata():
    rt = runtime()
    result = {'path': '/tmp/contract.txt', 'content': 'terms', 'eof': True}
    item = rt.normalize(record('read', result, {'path': '/tmp/contract.txt'}))['candidates'][0]
    assert item['content_version'] == ''
    result['mtime'] = 42
    item = rt.normalize(record('read', result, {'path': '/tmp/contract.txt'}))['candidates'][0]
    assert item['content_version'] == 'mtime:42'


def test_callback_retry_preserves_calls_and_marks_incomplete():
    rt = runtime()
    rt.request = Mock(side_effect=RuntimeError('offline'))
    rt.observe([record('native_search', {'results': []}, {'query': 'contract'})])
    assert rt.failed and len(rt.pending) == 1
    rt.request = Mock(return_value={})
    rt.flush()
    assert rt.request.call_args.kwargs['observations'][0]['incomplete']
    assert not rt.pending


def test_organizer_has_no_tools_and_uses_structured_task(monkeypatch):
    from lazymind.directory import api
    seen = []

    def run(request, **kwargs):
        assert kwargs['validate_output']({'targets': []}) == {'targets': []}
        seen.append(request)
        return SimpleNamespace(model_dump=lambda: {'status': 'succeeded', 'output': {'targets': []}})
    monkeypatch.setattr(api, 'run_llm_task', run)
    result = asyncio.run(api.plan(api.PlanRequest(mode='route', input={'resources': {}})))
    assert result['status'] == 'succeeded'
    assert seen[0].tools == [] and seen[0].mode == 'llm'
    assert seen[0].response_format == {'type': 'json_object'}


def test_directory_tools_execute_without_host_file_approval():
    from lazyllm.tools.agent import AuthorizationDecision
    from lazyllm.tools.agent.toolsManager import ToolManager
    from lazymind.chat.engine.agent_runtime.workspace_policy import WorkspaceAuthorizationPolicy

    rt = runtime()
    rt.request = Mock(return_value={'directories': []})
    manager = ToolManager(rt.tools())
    permission = SimpleNamespace(workflow_full_trust=False, active=False)
    prepared = manager.prepare_tool_calls([
        {'id': 'list', 'type': 'function', 'function': {'name': 'list_document_directories', 'arguments': '{}'}},
    ], authorization_policy=WorkspaceAuthorizationPolicy(permission, set(), lambda _: False))
    assert prepared[0].authorization is AuthorizationDecision.ALLOW
    result = manager.execute_prepared(prepared)
    assert result.records[0].disposition is ToolExecutionDisposition.EXECUTED
    rt.request.assert_called_once_with('list')


def test_account_identity_survives_chat_configuration_transport():
    import json
    from lazymind.chat.service.chat_request import ChatRuntimeOptions

    rt = runtime()
    scopes = rt.configuration.tool_config['document_directory_scopes']
    options = ChatRuntimeOptions(tool_config={'document_directory_scopes': json.dumps(scopes)})
    rt.configuration.tool_config = options.tool_config
    observation = rt.normalize(record('fs_feishu_read', 'real document text',
                                      {'path': 'https://example.feishu.cn/docx/token1'}))
    assert observation['candidates'][0]['namespace'] == 'tenant-user-1'
    assert observation['candidates'][0]['read_state'] == 'read'


@pytest.mark.parametrize('path', ['~node/token1', '/~node/token1', 'https://x.feishu.cn/wiki/token1'])
def test_feishu_actual_paths_and_outer_metadata(path):
    rt = runtime()
    searched = rt.normalize(record('fs_feishu_search', {'results': [
        {'title_highlighted': '<h>Contract</h> A',
         'result_meta': {'token': 'token1', 'url': 'https://x.feishu.cn/wiki/token1'}}]}))
    assert searched['candidates'][0]['title'] == 'Contract A'
    read = rt.normalize(record('fs_feishu_read', 'actual terms', {'path': path}))
    assert not read['incomplete']
    assert read['candidates'][0]['resource_id'] == 'token1'
    assert read['candidates'][0]['read_state'] == 'read'
    rt.configuration.names['fs_feishu_info'] = 'feishu'
    info = rt.normalize(record('fs_feishu_info', {'node_token': 'token1', 'title': 'Contract A'}, {'path': path}))
    assert info['operation'] == 'metadata'
    assert info['candidates'][0]['read_state'] == 'unread'


def test_notion_actual_mcp_envelope_and_url_only_fetch():
    rt = runtime()
    url = 'https://app.notion.com/p/3f4b8b30a5b08119bd8bcf0b4999c02f'
    page = {'url': url, 'title': 'Contract', 'text': 'Actual terms'}

    def wrap(value):
        return 'Tool call result:\nReceived text message:\n' + json.dumps(value)

    searched = rt.normalize(record('notion_search', wrap({
        'results': [{**page, 'id': '3f4b8b30-a5b0-8119-bd8b-cf0b4999c02f'}]})))
    fetched = rt.normalize(record('notion_fetch', wrap(page), {'id': url}))
    assert len(searched['candidates']) == 1
    item = fetched['candidates'][0]
    assert (item['title'], item['locator']['url'], item['read_state']) == ('Contract', url, 'read')
    assert item['resource_id'] == searched['candidates'][0]['resource_id']
    assert not fetched['incomplete']
    failed = rt.normalize(record('notion_fetch', 'Failed to call MCP tool "fetch": HTTP 403 Forbidden', {'id': url}))
    assert failed['status'] == 'denied' and failed['incomplete']
    assert all(c['read_state'] != 'read' and not c['excerpt'] for c in failed['candidates'])
    unknown = rt.normalize(record('notion_search', wrap('not JSON')))
    assert unknown['status'] == 'unknown' and unknown['incomplete']
    conflict = rt.normalize(record('notion_fetch', wrap(page), {'id': 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'}))
    assert not conflict['candidates'] and conflict['incomplete']


@pytest.mark.parametrize('wrapped', [False, True])
def test_document_json_cannot_replace_provider_identity(wrapped):
    body = '{"id":"different-resource","account_id":"different-account","title":"body field"}'
    result = {'ok': True, 'value': body} if wrapped else body
    obs = runtime().normalize(record('fs_feishu_read', result, {'path': '~node/token1'}))
    item = obs['candidates'][0]
    assert (item['namespace'], item['resource_id'], item['read_state']) == ('tenant-user-1', 'token1', 'read')
    assert item['title'] == '' and 'body field' in item['excerpt']


def test_failed_history_search_retains_its_source_and_scope():
    root = '/home/u/.claude/projects'
    obs = runtime().normalize(record('grep', {'ok': False, 'value': 'Path outside allowed root'},
                                     {'root': root, 'pattern': 'LazyMind'}))
    assert obs['source'] == 'agent_conversations'
    assert obs['scope']['root'] == root
    assert obs['status'] == 'failed' and obs['incomplete'] and not obs['candidates']


def test_directory_schema_errors_are_corrected_within_existing_retry_budget(monkeypatch):
    from lazymind.directory import api
    from lazymind.chat.service import llm_task
    prompts = []
    responses = iter(['{"targets":[{"directory_id":"d","resource_keys":[1]}]}',
                      '{"targets":[{"directory_id":"d","resource_keys":["key"]}]}'])

    def model(request, prompt, **kwargs):
        prompts.append(prompt)
        return next(responses)
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(llm_task, '_call_model', model)
    result = asyncio.run(api.plan(api.PlanRequest(mode='route', input={
        'resources': {'key': {}}, 'directories': [{'id': 'd', 'auto_add': True}]})))
    assert result['status'] == 'succeeded'
    assert result['output']['targets'][0]['resource_keys'] == ['key']
    assert result['usage']['model_calls'] == 2
    assert 'resource_keys' in prompts[1] and 'validation' in prompts[1].lower()


@pytest.mark.parametrize('assignment', [
    {'resource_key': 'unknown', 'group_id': '', 'confirmed': False, 'reason': ''},
    {'resource_key': 'key', 'group_id': 'missing', 'confirmed': False, 'reason': ''},
])
def test_organizer_rejects_resources_and_groups_outside_input(assignment):
    from lazymind.directory.api import PlanRequest, validate_plan
    with pytest.raises(ValueError):
        validate_plan(PlanRequest(mode='organize', input={'resources': {'key': {}}, 'groups': []}),
                      {'groups': [], 'assignments': [assignment]})


@pytest.mark.parametrize('basis', ['metadata', 'read', 'invented'])
def test_organizer_validates_confirmation_evidence_basis(basis):
    from lazymind.directory.api import PlanRequest, validate_plan
    request = PlanRequest(mode='organize', input={'resources': {'key': {}}, 'groups': []})
    output = {'groups': [], 'assignments': [{
        'resource_key': 'key', 'group_id': '', 'confirmed': True,
        'reason': 'Title and source identify the object and type', 'evidence_basis': basis,
    }]}
    if basis == 'invented':
        with pytest.raises(ValueError):
            validate_plan(request, output)
    else:
        assert validate_plan(request, output)['assignments'][0]['evidence_basis'] == basis


def test_execution_facts_distinguish_summaries_metadata_and_message_windows():
    rt = runtime()
    rt.request = Mock(return_value={})

    def read(call, path, content):
        item = record('read', {
            'content': content, 'line_unit': 'physical', 'offset': 10, 'end_line': 12, 'eof': False,
        }, {'path': path})
        item.call_id = call
        return item
    rt.observe([read('notes', '/home/u/.codex/memories/summary.md', 'Old work remains pending.')])
    rt.observe([read('meta', '/home/u/.codex/sessions/a.jsonl', '{"type":"session_meta","payload":{"id":"s"}}')])
    assert rt._facts['local']['reads'] == 1
    assert rt._facts['agent_conversations']['message_windows'] == 0
    message = read('message', '/home/u/.codex/sessions/a.jsonl',
                   '10: {"type":"response_item","payload":{"type":"message","role":"user",'
                   '"content":[{"type":"input_text","text":"Project decision"}]}}')
    rt.observe([message, message])
    assert rt._facts['agent_conversations']['message_windows'] == 1
    assert 'Project decision' not in rt.model_context()
    assert '/home/u/.codex/sessions/a.jsonl' in rt.model_context()
    rt.request = Mock(side_effect=RuntimeError('offline'))
    zero = record('fs_google_search', [])
    zero.call_id = 'zero'
    rt.observe([zero])
    assert rt._facts['google_drive']['zero_hits'] == 1
    assert json.loads(rt.model_context().split('\n')[-1])['capture_gap'] is True
    rt.request = Mock(return_value={})
    rt.finish()
    assert rt.model_context() is None


def test_execution_facts_do_not_count_failed_skipped_or_metadata_as_body_reads():
    rt = runtime()
    rt.request = Mock(return_value={})
    failed = record('fs_feishu_search', {'ok': False, 'value': 'denied'})
    failed.call_id = 'failed'
    skipped = record('read', 'not executed', {'path': '/tmp/a'})
    skipped.call_id = 'skipped'
    skipped.disposition = ToolExecutionDisposition.SKIPPED
    rt.observe([failed, skipped])
    assert rt._facts['feishu']['failures'] == 1 and rt._facts['feishu']['searches'] == 0
    assert rt._facts['local']['reads'] == 0 and rt._facts['local']['not_executed'] == 1


def test_execution_facts_retain_capture_failure_without_inventing_reads():
    rt = runtime()
    rt.request = Mock(return_value={})
    rt.normalize = Mock(side_effect=ValueError('unsupported envelope'))
    rt.observe([record('read', {'unexpected': True}, {'path': '/tmp/a'})])
    assert rt._facts['unknown']['unparsed'] == 1
    assert rt._facts['unknown']['reads'] == 0
    assert json.loads(rt.model_context().split('\n')[-1])['capture_gap'] is True


def test_initial_capture_marker_failure_remains_visible_after_successful_flush():
    rt = runtime()
    rt.request = Mock(side_effect=[RuntimeError('offline'), {}])
    rt.observe([record('fs_google_search', [])])
    assert not rt.failed and not rt.pending
    assert rt._facts['google_drive']['zero_hits'] == 1
    assert json.loads(rt.model_context().split('\n')[-1])['capture_gap'] is True


def test_executed_history_search_with_unparsed_hits_is_search_not_body_verification():
    rt = runtime()
    rt.request = Mock(return_value={})
    rt.observe([record('grep', {
        'status': 'ok', 'results': [{'path': '/home/u/.codex/sessions/a.jsonl', 'line': '42', 'text': 'match'}],
    }, {'path': '/home/u/.codex/sessions', 'pattern': 'project'})])
    facts = rt._facts['agent_conversations']
    assert facts['searches'] == 1 and facts['zero_hits'] == 0
    assert facts['unparsed'] == 1 and facts['message_windows'] == 0


def test_physical_clipped_messages_and_unparsed_shell_are_not_verified():
    rt = runtime()
    rt.request = Mock(return_value={})
    path = '/home/u/.codex/sessions/a.jsonl'
    message = json.dumps({'type': 'response_item', 'payload': {
        'type': 'message', 'role': 'user', 'content': 'decision'}})
    result = {'content': message, 'line_unit': 'physical', 'offset': 5, 'end_line': 5,
              'eof': True, 'truncated': True, 'truncated_lines': [5]}
    item = record('read', result, {'path': path})
    rt.observe([item])
    assert rt._facts['agent_conversations']['message_windows'] == 0
    candidate = rt.normalize(item)['candidates'][0]
    assert candidate['locator']['truncated_lines'] == [5]
    assert candidate['locator']['range_kind'] == 'physical_lines'
    shell = record('shell', {
        'ok': True, 'value': {'stdout': message, 'path': path, 'offset': 5, 'end_line': 5},
    }, {'cmd': 'python arbitrary.py'})
    shell.call_id = 'shell'
    rt.observe([shell])
    assert rt.normalize(shell)['status'] == 'unparsed'
    assert rt.normalize(shell)['candidates'] == []
    assert rt._facts['local']['unparsed'] == 1


def test_old_logical_locator_is_discovery_only():
    rt = runtime()
    rt.request = Mock(return_value={'entries': [{'locator': {
        'path': '/home/u/.codex/sessions/a.jsonl', 'range_kind': 'reader_logical_lines',
        'offset': 172, 'end_line': 173}}]})
    locator = rt.open_document_directory('owned')['entries'][0]['locator']
    assert locator['path'].endswith('a.jsonl')
    assert 'offset' not in locator and 'end_line' not in locator
    assert locator['range_kind'] == 'invalid_legacy_locator'


def test_evidence_context_keeps_all_source_counts_before_optional_scopes():
    rt = runtime()
    for source in ['local', 'agent_conversations', 'feishu', 'notion', 'google_drive', 'mail']:
        rt._facts[source] = dict(
            searches=200, reads=200, zero_hits=1, failures=2, not_executed=3,
            incomplete=4, unparsed=5, message_windows=6, non_message_reads=7,
            recent_calls=[{'call_id': 'c', 'scope': {'query': 'x' * 2000}, 'ranges': []}],
        )
    rendered = rt.model_context()
    assert len(rendered) <= 1500
    facts = json.loads(rendered[rendered.index('{'):])
    assert set(facts['sources']) == set(rt._facts)
    assert all(value['message_windows'] == 6 for value in facts['sources'].values())


def test_empty_read_past_eof_does_not_invent_a_history_window():
    rt = runtime()
    result = rt.normalize(record('read', {
        'content': '', 'line_unit': 'physical', 'offset': 100, 'end_line': 99, 'eof': True,
    }, {'path': '/home/u/.codex/sessions/a.jsonl'}))
    assert result['candidates'] == [] and result['incomplete']


def test_google_drive_enumeration_read_capture_matches_core_fixture(monkeypatch):
    from pathlib import Path
    from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS
    from lazymind.directory.folders import cloud_page

    fixture = json.loads((Path(__file__).parents[2] / 'fixtures/document_directory_drive.json').read_text())
    fs = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    monkeypatch.setattr(fs, '_get', lambda *a, **kw: {'files': [fixture['metadata']]})
    nodes, children, cursor, _ = cloud_page(
        fixture['source'], {'id': 'FOLDER_789', 'key': 'root', 'path': ''}, fs)
    assert nodes == [fixture['node']]
    assert not children and cursor is None
    monkeypatch.setattr(fs, '_get', lambda *a, **kw: fixture['metadata'])
    monkeypatch.setattr(fs, '_export_document', lambda *a: fixture['body'].encode())
    for case in fixture['cases']:
        body = fs.read(case['path'])
        assert body == fixture['body']
        item = record('fs_google_read', body, {'path': case['path']})
        item.call_id = case['observation']['call_id']
        observation = runtime().normalize(item)
        assert observation['candidates'][0]['read_at']
        observation['candidates'][0]['read_at'] = case['observation']['candidates'][0]['read_at']
        assert observation == case['observation']



def test_google_drive_invalid_paths_keep_other_candidates_and_report_gap():
    observation = runtime().normalize(record('fs_google_search', [
        {'path': '/folder/file'}, {'path': '/drive/drive-1/file-1'}, {'name': 'file-2', 'google_drive_path': 'googledrive:/file-2'},
    ]))
    assert observation['incomplete']
    assert [c['resource_id'] for c in observation['candidates']] == ['file-1', 'file-2']

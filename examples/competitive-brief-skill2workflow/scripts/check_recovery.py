"""Read-only consistency checks over the original real-service recordings.

These checks issue no API calls and cannot turn the recorded assisted recovery
into a new runtime test, an autonomous success, or an edit/approval success.
"""
import json
from pathlib import Path

EVIDENCE = Path(__file__).resolve().parents[1] / 'evidence'


def load_records(root=EVIDENCE):
    names = ['http/projection-failed.json', 'http/projection-completed.json',
             'http/tree-status-8.json', 'http/external-after-recovery.json',
             'http/retry-command.json', 'requests/retry-analysis-request.json',
             'http/retry-transport.json', 'http/slots-synthesis.json', 'http/slots-final.json',
             'http/edit-response.json', 'http/slot-edit-response.json']
    return {name: json.loads((root / name).read_text()) for name in names}


def payload(record):
    response = record['response']
    return response.get('result', response.get('data', response))


def require(ok, message):
    if not ok:
        raise ValueError(message)


def check(records):
    failed = payload(records['http/projection-failed.json'])
    final = payload(records['http/projection-completed.json'])
    before = payload(records['http/tree-status-8.json'])
    after = payload(records['http/external-after-recovery.json'])
    request = records['requests/retry-analysis-request.json']
    command = payload(records['http/retry-command.json'])
    require(before['status'] == after['status'] == 'failed' and not after['result_ready'],
            'Original external task failure must survive recovery')
    require(before['task_id'] == after['task_id'], 'External task identity changed')
    require(before['progress']['completed_steps'] == 2, 'Autonomous result was not 2/5')
    require(failed['session_id'] == final['session_id'] == request['session_id'] == command['SessionID'],
            'Recovery belongs to a different session')
    require(failed['graph_hash'] == final['graph_hash'] and failed['graph'] == final['graph'],
            'Recovery changed the published graph')
    require('analyze_positioning' in failed['projection']['retryable'], 'Failure was not retryable')
    nodes = final['projection']['nodes']
    require(final['status'] == 'completed' and final['projection']['completed'] and
            final['projection']['end_reached'] and len(nodes) == 5 and
            all(n['execution'] == 'succeeded' for n in nodes.values()), 'Session not completed 5/5')
    require(all(not n['requires_approval'] for n in nodes.values()), 'Unexpected human approval claim')
    attempts = final['attempt_history']['analyze_positioning']
    require([a['status'] for a in attempts] == ['failed', 'succeeded'], 'Failed attempt was lost')
    require(attempts[0]['attempt_id'] == failed['attempt_history']['analyze_positioning'][0]['attempt_id'],
            'Original failed attempt identity lost')
    for step in ('collect_request', 'research_competitors'):
        require(final['attempt_history'][step] == failed['attempt_history'][step],
                'Upstream work reran: ' + step)
    prior_inputs = failed['input_witnesses'][attempts[0]['attempt_id']]
    retry_inputs = final['input_witnesses'][attempts[1]['attempt_id']]
    require(prior_inputs == retry_inputs and {x['material_id'] for x in retry_inputs} ==
            {'competitor_research', 'request_brief'}, 'Retry did not reuse original input revisions')
    require(request['retry_origin'] == 'automatic' and len(request['steps']) == 1 and
            request['steps'][0]['step_id'] == 'analyze_positioning', 'Retry scope/origin changed')
    accepted = command['ResponseJSON']
    require(command['CommandID'] == request['command_id'] == accepted['command_id'] and
            accepted['accepted'] and accepted['task_id'] == attempts[1]['task_id'],
            'Accepted command does not match the successful retry')
    require('socket.timeout' in records['http/retry-transport.json']['outcome'], 'Lost timeout observation')
    require(records['http/edit-response.json']['http_status'] == 503 and
            records['http/slot-edit-response.json']['http_status'] == 500, 'Edit failures changed')
    def report(name):
        return next(s for s in payload(records[name])['slots'] if s['slot_id'] == 'competitive_brief')
    old_report, new_report = report('http/slots-synthesis.json'), report('http/slots-final.json')
    require(old_report['revision'] == new_report['revision'] == 1 and
            old_report['artifact_id'] == new_report['artifact_id'] and
            old_report['artifact_value'] == new_report['artifact_value'], 'Failed edits changed report')
    return {'mode': 'recorded_evidence_replay', 'autonomous_external_task': 'failed',
            'assisted_session': 'completed', 'prior_inputs_reused': True,
            'human_approval_verified': False, 'artifact_edit_succeeded': False,
            'new_live_runs': 0}


if __name__ == '__main__':
    print(json.dumps(check(load_records()), indent=2))

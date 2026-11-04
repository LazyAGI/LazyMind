"""Chat-owned question checkpoints backed by selected Workflow artifacts."""

import json
import uuid

from lazymind.workflow_sdk import WorkflowClientError


class InputCheckpoint:
    def __init__(self, toolkit, policy, history, query):
        self.toolkit = toolkit
        self.policy = policy
        self.history = history or []
        self.query = query
        self.emitted = set()

    def config(self):
        return (self.policy() or {}).get('input_checkpoint') or {}

    def protects(self, slot):
        return slot == self.config().get('slot')

    def _read(self, session_id, required=False):
        config = self.config()
        if not config:
            return None
        matches = [a for a in self.toolkit.list_artifacts(session_id).get('artifacts') or []
                   if a.get('slot') == config['slot']]
        if not matches and not required:
            return None
        if len(matches) != 1:
            raise WorkflowClientError('WORKFLOW_CHECKPOINT_MISSING', 'Missing information checklist is unavailable.')
        artifact = matches[0]
        artifact_id = artifact.get('artifact_id') or artifact.get('id')
        current = self.toolkit.read_artifact(artifact_id)
        value = current.get('value')
        if isinstance(value, dict) and 'questions' not in value:
            value = value.get('data', value.get('text', value))
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except ValueError:
                value = None
        if not isinstance(value, dict) or not isinstance(value.get('questions'), list):
            raise WorkflowClientError('WORKFLOW_CHECKPOINT_INVALID', 'Checklist must contain a questions array.')
        token = value.get('checkpoint_token')
        trusted_agent_patch = current.get('change_source') == 'agent' and token and (
            token in self.emitted or self._has_receipt(session_id, token)
        )
        if (value.get('answer_text') or token) and not (
            current.get('change_source') == 'human' or trusted_agent_patch
        ):
            raise WorkflowClientError('WORKFLOW_CHECKPOINT_INVALID', 'Only the host may record checkpoint answers.')
        return artifact_id, {**artifact, **current}, value

    def _save(self, record, value):
        artifact_id, current, _ = record
        return self.toolkit.patch_artifact(
            artifact_id, int(current.get('revision') or 0), value, 'json',
            str(current.get('caption') or ''), base_draft_version=int(current.get('draft_version') or 0),
        )

    def guard(self, session_id, targets=(), completed_step=None):
        config = self.config()
        if not config:
            return None
        after = config['after_step']
        if completed_step != after and not set(targets).intersection(config.get('before_steps') or []):
            return None
        record = self._read(session_id, required=completed_step == after)
        if record is None:
            state = self.toolkit.get_workflow_state(session_id)
            node = ((state.get('projection') or {}).get('nodes') or {}).get(after) or {}
            if node.get('execution') == 'succeeded':
                raise WorkflowClientError('WORKFLOW_CHECKPOINT_MISSING', 'Completed collection has no checklist.')
            return None
        _, _, value = record
        if not value['questions'] or value.get('answer_text'):
            return None
        from lazymind.chat.engine.tools.ask_user import _normalise_questions, emit_question_card

        # Validate before persisting a pending card. Never silently truncate a
        # long checklist: all missing fields must remain visible to the user.
        questions = _normalise_questions(value['questions'])
        token = value.get('checkpoint_token') or str(uuid.uuid4())
        if not value.get('checkpoint_token'):
            self._save(record, {**value, 'checkpoint_token': token})
        if token not in self.emitted:
            emit_question_card(
                questions,
                title=config.get('title') or '补充缺失信息',
                description='已完成素材搜集，请补充以下信息；不确定的内容可填写“不知道”或“省略”。',
            )
            self.emitted.add(token)
        return {
            'status': 'waiting_for_input', 'session_id': session_id,
            'checkpoint_token': token, 'checkpoint_slot': config['slot'],
            'questions': questions,
            'next_action': {'tool': 'answer_workflow_questions',
                            'instruction': 'Wait for the user answer in a new turn, then save it and continue.'},
            '_agent_control': {'stop': True, 'reason': 'workflow_input_checkpoint',
                               'final_text': '素材已搜集完成，请补充卡片中的缺失信息，回答后继续生成。'},
        }

    def _has_receipt(self, session_id, token):
        for message in self.history:
            if not isinstance(message, dict) or message.get('role') != 'tool':
                continue
            content = message.get('content')
            if isinstance(content, str):
                try:
                    content = json.loads(content)
                except ValueError:
                    continue
            if isinstance(content, dict) and content.get('ok') is True and 'value' in content:
                content = content['value']
                if isinstance(content, str):
                    try:
                        content = json.loads(content)
                    except ValueError:
                        continue
            if isinstance(content, dict) and token and (
                content.get('checkpoint_token') == token and content.get('session_id') == session_id
                and content.get('status') == 'waiting_for_input'
            ):
                return True
        return False

    def answer(self, session_id):
        record = self._read(session_id, required=True)
        if record is None:
            raise WorkflowClientError('WORKFLOW_CHECKPOINT_MISSING', 'No question checkpoint is configured.')
        _, _, value = record
        token = value.get('checkpoint_token')
        if value.get('answer_text'):
            return {'status': 'already_answered', 'session_id': session_id}
        # Only a prior turn's durable card can be answered. In particular the
        # launch request must never be mistaken for the answer in the same turn.
        receipt = self._has_receipt(session_id, token)
        if not receipt or token in self.emitted or not str(self.query or '').strip():
            raise WorkflowClientError('WORKFLOW_CHECKPOINT_ANSWER_REQUIRED', 'Wait for the user to answer the card.')
        self._save(record, {**value, 'answer_text': str(self.query).strip()})
        return {'status': 'answered', 'session_id': session_id,
                'instruction': 'Answers saved. Call get_ready_steps and continue without repeating collection.'}


CHECKPOINT_GUIDANCE = (
    'A declared input_checkpoint is an additional post-collection clarification, independent of startup Ask limits. '
    'Execution tools automatically show its question card and stop when facts remain missing. '
    'On the NEXT user turn, if the user answers that card (including unknown/omit), call '
    'answer_workflow_questions() to save the actual user reply, then get_ready_steps and continue. '
    'Do not call it for a status question, cancellation or unrelated request. Do not invent answers, '
    'patch the checkpoint yourself, rerun collection to bypass questions, or create a new session. '
)

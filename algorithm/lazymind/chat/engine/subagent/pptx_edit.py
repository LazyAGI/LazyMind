"""Restricted tools for editing a Workflow deck through an ordinary SubAgent."""
import json
from typing import Any, Dict, List, Optional
from typing_extensions import NotRequired, TypedDict

from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.engine.tools.artifact_output import artifact_output

from .context import require_context
from . import tools


class TextEdit(TypedDict):
    expected_text: str
    text: str
    id: NotRequired[str]
    page: NotRequired[int]


class SpeakerNote(TypedDict):
    page: int
    text: str


class StyleEdit(TypedDict, total=False):
    pages: List[int]
    slot_ids: List[str]
    text_scope: str
    target_font: str
    font_size_pt: float
    bold: bool
    italic: bool
    underline: bool
    text_color: str
    allow_download: bool


def enforce_failure_budget(event, failures):
    """End the task after two failures of one tool, not just block its execution."""
    for item in event.get('tool_results') or []:
        name = item.get('name')
        if name not in {'inspect_current_pptx', 'read_pptx_edit_material', 'publish_pptx_edit'}:
            continue
        result = item.get('result', item.get('content'))
        if isinstance(result, str):
            try:
                result = json.loads(result)
            except ValueError:
                result = {'ok': False}
        failed = not isinstance(result, dict) or result.get('ok') is False or result.get('status') == 'error'
        failures[name] = failures.get(name, 0) + 1 if failed else 0
        if failures[name] >= 2:
            raise ToolExecutionError(
                f'{name} failed twice; editing stopped without further retries. Original PPTX is retained.')


def _session() -> str:
    ctx = require_context()
    session = str(ctx.params.get('_pptx_edit_session_id') or '')
    if ctx.agent_type != 'pptx_edit' or not session:
        raise ToolExecutionError('A bound PPTX editing task is required')
    return session


@fc_register(host_file='NONE')
def inspect_current_pptx(page: int = 1) -> Dict[str, Any]:
    """Read ONE page's editable text and speaker notes. Pages are 1-based.

    Copy exact slot IDs, never guess. For all-page speeches read each page using
    next_page until null. Layout/font diagnostics are intentionally omitted.
    """
    result = tools.edit_presentation('native_pptx', 'inspect_content', pages=page,
                                     workflow_session_id=_session())
    return {'source_sha256': result['sha256'], 'slide_count': result['slide_count'],
            'pages': [{'page': p['page'],
                       'next_page': p['page'] + 1 if p['page'] < result['slide_count'] else None,
                       'slots': [{'id': s['id'], 'text': s['text'],
                                  **{k: s[k] for k in ('shape_name', 'shape_id') if s.get(k) is not None}}
                                 for s in p['slots']],
                       'speaker_notes': p['speaker_notes']} for p in result['pages']]}


@fc_register(host_file='NONE')
def read_pptx_edit_material(slot: str = 'material_summary') -> Dict[str, Any]:
    """Read existing evidence only: material_summary or requirement_analysis; no research."""
    if slot not in {'material_summary', 'requirement_analysis'}:
        raise ToolExecutionError('Only saved material_summary and requirement_analysis may be read')
    rows = tools._public_workflow_artifacts(_session(), slot)
    rows = [row for row in rows if row.get('selected') and row.get('validity') == 'effective'
            and not row.get('deleted')]
    values = []
    for row in rows:
        artifact_id = row.get('artifact_id') or row.get('id')
        if not artifact_id:
            raise ToolExecutionError('Saved material has no artifact reference; do not guess IDs')
        values.append(tools._workflow_client().read_artifact(artifact_id).result)
    return artifact_output({'artifacts': values})


@fc_register(host_file='NONE')
def publish_pptx_edit(source_sha256: str,
                      text_edits: Optional[List[TextEdit]] = None,
                      speaker_notes: Optional[List[SpeakerNote]] = None,
                      require_all_notes: bool = False,
                      target_font: Optional[str] = None,
                      pages: Optional[List[int]] = None,
                      slot_ids: Optional[List[str]] = None,
                      text_scope: str = 'all',
                      allow_font_download: bool = False,
                      style_edits: Optional[List[StyleEdit]] = None) -> Dict[str, Any]:
    """Publish sparse text edits and/or notes once, then finish the task.

    Use the inspected source_sha256. text_edits: [{id,expected_text,text}],
    or [{page,expected_text,text}] when the original text uniquely matches one
    editable text slot on that page. Never guess IDs; ambiguous text is rejected.
    speaker_notes: [{page,text}] with 1-based pages. Set require_all_notes=true
    for every-page speeches. Preserve all unspecified text, formatting and notes.
    For font edits set target_font: omit pages/slot_ids for all slides, pages=[1]
    for page one, or exact inspected slot_ids for selected text. text_scope can
    be all/titles/body (titles matches native title placeholders; use slot_ids
    for custom text-box titles). Keep font size, weight and content unchanged.
    Set allow_font_download only when user asks to download missing open fonts.
    style_edits applies differently scoped text formatting in ONE revision, e.g.
    [{pages:[1],text_scope:'titles',font_size_pt:28,bold:true},
     {slot_ids:['4:3'],italic:true,underline:false,text_color:'#CC0000'}].
    Each operation may also set target_font and allow_download. Omit a property
    to preserve it; false explicitly cancels bold/italic/underline. PPTX sizes
    are points (pt), never pixels. Inspect exact slots for custom titles/parts.
    Do not regenerate text or resize boxes to satisfy a formatting-only request.
    """
    session = _session()
    ctx = require_context()
    if ctx.local_artifacts(['pptx_edit_receipt']):
        raise ToolExecutionError('This task already published its edit; do not publish twice')
    result = tools.edit_presentation(
        'native_pptx', 'edit_content', source_sha256=source_sha256,
        text_edits=text_edits, speaker_notes=speaker_notes,
        require_all_notes=require_all_notes, workflow_session_id=session,
        target_font=target_font, pages=pages, slot_ids=slot_ids,
        text_scope=text_scope, allow_font_download=allow_font_download,
        style_edits=style_edits,
    )
    if result.get('status') != 'ok' or not result.get('workflow_artifact'):
        raise ToolExecutionError('PPTX publication did not return a successful artifact revision')
    # This receipt satisfies the SubAgent completion contract. The file itself
    # remains authoritative in the Workflow slot; never create a parallel deck.
    result = artifact_output(result)
    value = {'data': result['workflow_artifact']}
    seq = ctx.next_artifact_seq('pptx_edit_receipt')
    ctx.record_local_artifact('pptx_edit_receipt', 'json', value, seq)
    ctx.emit({'type': 'artifact', 'slot': 'pptx_edit_receipt',
              'content_type': 'json', 'value': value, 'seq': seq})
    return result


def edit_tools():
    return [inspect_current_pptx, read_pptx_edit_material, publish_pptx_edit]

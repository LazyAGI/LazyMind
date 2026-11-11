"""Read model response bodies without treating provider metadata as business data."""
from dataclasses import dataclass


@dataclass(frozen=True)
class ModelResponse:
    body: str | dict
    reasoning: str = ''


def _reasoning_text(message):
    for field in ('reasoning_content', 'reasoning'):
        text = message.get(field)
        if isinstance(text, str) and text:
            return text
    details = message.get('reasoning_details')
    if isinstance(details, dict):
        details = [details]
    texts = []
    for item in details if isinstance(details, list) else []:
        if not isinstance(item, dict):
            continue
        text = item.get('text') or item.get('summary')
        if isinstance(text, str):
            texts.append(text)
    return ''.join(texts)


def normalize_response(raw):
    """Read provider fields verbatim; never recover executable text from reasoning."""
    if isinstance(raw, dict):
        if ('content' not in raw and not {'reasoning_content', 'reasoning_details'} & raw.keys()
                and not (raw.get('role') == 'assistant' and 'reasoning' in raw)):
            return ModelResponse(raw)
        return ModelResponse(str(raw.get('content') or ''), _reasoning_text(raw))
    return ModelResponse(str(raw or ''))

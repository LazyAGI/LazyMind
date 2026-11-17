"""Exercise real review selection through the Responses transport and Writer formatter."""
import copy
import json
from io import BytesIO

import pytest
import requests
from lazyllm.module.llms.onlinemodule.supplier.openai_responses import OpenAIResponsesChat
from pydantic import ValidationError

from lazymind.document_tools.paper import literature_review
from lazymind.document_tools.paper.review import materials


PAPERS = [
    {'id': 'paper-a', 'title': 'First paper', 'text': 'Evidence from the first paper.'},
    {'id': 'paper-b', 'title': 'Second paper', 'text': 'Evidence from the second paper.'},
]
SELECTION = {'queries': {}, 'external_queries': {}, 'material_roles': {'M1': 'paper', 'M2': 'paper'}}


@pytest.fixture
def select_from_responses(monkeypatch):
    """Replace only provider IO/configuration, keeping parsing and validation real."""
    model = OpenAIResponsesChat(base_url='https://provider.test/v1/', model='test',
                               api_key='test', stream=False)
    monkeypatch.setattr(materials, 'AutoModel', lambda **_: model)
    monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)

    def select(payload):
        requests_sent = []

        def send(session, request, **kwargs):
            assert request.url == 'https://provider.test/v1/responses'
            requests_sent.append(json.loads(request.body))
            response = requests.Response()
            response.status_code = 200
            response._content = json.dumps({'status': 'completed', 'output': [{
                'type': 'message', 'id': 'msg_selection', 'role': 'assistant', 'status': 'completed',
                'content': [{'type': 'output_text', 'text': json.dumps(payload), 'annotations': []}],
            }]}).encode()
            response._content_consumed = True
            response.raw = BytesIO()
            return response

        monkeypatch.setattr(requests.Session, 'send', send)
        result = materials._select(copy.deepcopy(PAPERS), {'topic': 'Evidence synthesis'}, [], count_range=(1, 10))
        return result, requests_sent

    return select


def test_valid_review_selection_survives_responses_metadata(select_from_responses):
    (selected, plan), sent = select_from_responses(copy.deepcopy(SELECTION))
    assert selected == PAPERS
    assert plan.model_dump(exclude_none=True) == SELECTION
    assert '_lazyllm_provider_items' not in plan.model_dump_json()
    assert len(sent) == 1, 'Valid model JSON must not require a correction retry'


@pytest.mark.parametrize(('patch', 'location', 'error_type'), [
    ({'external_query': {}}, ('external_query',), 'extra_forbidden'),
    ({'material_roles': {'M1': 'paper', 'M2': 'paper', 'M9': 'paper'}},
     ('material_roles', 'M9'), 'extra_forbidden'),
    ({'material_roles': {'M1': 'paper'}}, ('material_roles', 'M2'), 'missing'),
    ({'material_roles': {'M1': 'invented', 'M2': 'paper'}}, ('material_roles', 'M1'), 'literal_error'),
    ({'external_queries': {'evidence': 0}}, ('external_queries', 'evidence'), 'greater_than_equal'),
    ({'external_queries': {'evidence': 201}}, ('external_queries', 'evidence'), 'less_than_equal'),
])
def test_review_selection_still_rejects_invalid_business_data(select_from_responses, patch, location, error_type):
    with pytest.raises(ValueError, match='Failed to validate LLM output as ReviewSelection') as failure:
        select_from_responses({**copy.deepcopy(SELECTION), **patch})
    cause = failure.value.__cause__
    assert isinstance(cause, ValidationError)
    assert [(error['loc'], error['type']) for error in cause.errors()] == [(location, error_type)]

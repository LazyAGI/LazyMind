from lazymind.chat.service.llm_task import LLMTaskRequest, _workflow_prompt


def test_workflow_generation_does_not_invent_required_attachments() -> None:
    prompt = _workflow_prompt(LLMTaskRequest(task_type='workflow.design_brief'))

    assert 'Do not invent user uploads' in prompt
    assert 'design it as optional' in prompt
    assert 'exact filenames listed by the runtime' in prompt


def test_workflow_repair_prompt_stays_domain_neutral() -> None:
    prompt = _workflow_prompt(LLMTaskRequest(task_type='workflow.repair'))

    assert 'final full content for every repaired core file' in prompt
    assert 'multi-page composite control' not in prompt
    assert 'widgetType: html-slide' not in prompt
    assert 'PPT' not in prompt

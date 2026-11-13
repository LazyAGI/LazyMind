from lazymind.chat.workflow.external_model import model_config


def test_external_transport_uses_attempt_lease_and_preserves_other_config():
    original = {'llm': {'model': 'existing'}}
    assert model_config({}, 'http://core', 'attempt', 'lease', original) is original
    for executor in ('external-agent', 'codex'):
        config = model_config({'metadata': {'model_executor': executor}},
                              'http://core', 'attempt', 'lease', original)
        assert set(config) == {'llm', 'vlm', 'evo_llm'}
        for role in config.values():
            assert role['model'] == 'external-agent'
            assert role['timeout'] == (15, 600)
            assert role['api_key'] == 'lease'
            assert role['base_url'] == 'http://core/internal/workflow-attempts/attempt/model/v1'
    assert original == {'llm': {'model': 'existing'}}

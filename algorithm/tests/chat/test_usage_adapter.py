from lazymind.chat.service.usage_adapter import adapt_provider_usage


def test_openai_details_maps_cached_zero_as_observable():
    adapted = adapt_provider_usage({
        'prompt_tokens': 11062,
        'completion_tokens': 61,
        'total_tokens': 11123,
        'prompt_tokens_details': {'cached_tokens': 0},
        'completion_tokens_details': {'reasoning_tokens': 42},
    })
    assert adapted == {
        'input_tokens': 11062,
        'output_tokens': 61,
        'total_tokens': 11123,
        'cached_tokens': 0,
        'reasoning_tokens': 42,
    }


def test_openai_details_maps_cached_hit():
    adapted = adapt_provider_usage({
        'provider_usage': {
            'prompt_tokens': 15179,
            'completion_tokens': 502,
            'prompt_tokens_details': {'cached_tokens': 1216},
            'completion_tokens_details': {'reasoning_tokens': 262},
        },
    })
    assert adapted['cached_tokens'] == 1216
    assert adapted['reasoning_tokens'] == 262
    assert adapted['input_tokens'] == 15179


def test_missing_cache_fields_are_unknown():
    adapted = adapt_provider_usage({'prompt_tokens': 10, 'completion_tokens': 2})
    assert adapted == {
        'input_tokens': 10,
        'output_tokens': 2,
        'total_tokens': 12,
    }
    assert 'cached_tokens' not in adapted
    assert 'reasoning_tokens' not in adapted


def test_prompt_cache_hit_schema_does_not_use_model_name():
    adapted = adapt_provider_usage({
        'prompt_tokens': 100,
        'completion_tokens': 20,
        'prompt_cache_hit_tokens': 80,
        'prompt_cache_miss_tokens': 20,
    })
    assert adapted['cached_tokens'] == 80
    assert adapted['input_tokens'] == 100


def test_anthropic_cache_read_schema():
    adapted = adapt_provider_usage({
        'input_tokens': 40,
        'output_tokens': 8,
        'cache_read_input_tokens': 0,
    })
    assert adapted['cached_tokens'] == 0
    assert adapted['input_tokens'] == 40
    assert adapted['output_tokens'] == 8


def test_provider_usages_unwrap_nested_provider_usage_envelopes():
    adapted = adapt_provider_usage({
        'provider_usages': [
            {
                'prompt_tokens': 24000,
                'completion_tokens': 431,
                'provider_usage': {
                    'prompt_tokens': 24000,
                    'completion_tokens': 431,
                    'prompt_cache_hit_tokens': 19200,
                    'prompt_cache_miss_tokens': 4800,
                },
            },
        ],
    })
    assert adapted['cached_tokens'] == 19200
    assert adapted['input_tokens'] == 24000


def test_provider_usages_are_summed_across_calls():
    adapted = adapt_provider_usage({
        'prompt_tokens': 150,
        'completion_tokens': 30,
        'provider_usages': [
            {
                'prompt_tokens': 100,
                'completion_tokens': 10,
                'prompt_tokens_details': {'cached_tokens': 80},
            },
            {
                'prompt_tokens': 50,
                'completion_tokens': 20,
                'prompt_tokens_details': {'cached_tokens': 0},
            },
        ],
    })
    assert adapted['input_tokens'] == 150
    assert adapted['output_tokens'] == 30
    assert adapted['cached_tokens'] == 80
    assert 'cache_input_tokens' not in adapted

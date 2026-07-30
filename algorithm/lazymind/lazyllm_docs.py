import importlib


_DOC_MODULES = (
    'common',
    'components',
    'configs',
    'flow',
    'hook',
    'launcher',
    'module',
    'patch',
    'prompt_template',
    'tools',
    'tracing',
    'utils',
)


def ensure_lazyllm_docs(lazyllm) -> bool:
    """Initialize LazyLLM's runtime docs when its package did not ship them prebuilt."""
    if lazyllm.add_doc.__doc__ and 'Add document' in lazyllm.add_doc.__doc__:
        return False

    original_init_doc = lazyllm.config['init_doc']
    try:
        # LazyLLM normally sets this before importing its doc modules. Temporarily
        # mirror that state so optional dependencies are not required just to add
        # their documentation.
        lazyllm.config['init_doc'] = True
        dependency_check = importlib.import_module('lazyllm.thirdparty').check_dependency_by_group
        dependency_check.cache_clear()
        for module_name in _DOC_MODULES:
            importlib.import_module(f'lazyllm.docs.{module_name}')
    finally:
        lazyllm.config['init_doc'] = original_init_doc

    return True

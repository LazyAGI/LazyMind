"""Composition root for installed trusted Host capabilities.

Names identify an installed implementation, not a workflow. Every package can
request a capability through its pinned runtime contract. Unknown capabilities
are rejected instead of silently switching behavior.
"""


def apply_host_extensions(contribution, context, config, providers=None):
    names = (contribution.runtime_policy or {}).get('host_extensions') or []
    if not names:
        return contribution
    if providers is None:
        from .product_project import contribute
        providers = {'product-project-v1': contribute}
    for name in dict.fromkeys(names):
        provider = providers.get(name)
        if provider is None:
            raise ValueError(f'Host extension is not installed: {name}')
        tools, stop_tools = provider(context, config, getattr(contribution, 'bind_successor', None))
        contribution.tools.extend(tools)
        contribution.stop_tools.extend(stop_tools)
    return contribution

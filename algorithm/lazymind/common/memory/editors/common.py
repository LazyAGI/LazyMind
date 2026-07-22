from __future__ import annotations

from typing import Any

import yaml

from ..schema.common import parse_yaml_frontmatter


def render_yaml_frontmatter(frontmatter: dict[str, Any]) -> str:
    body = yaml.safe_dump(
        frontmatter,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    ).strip()
    return f'---\n{body}\n---\n'


def set_nested_field(data: dict[str, Any], field: str, value: Any) -> None:
    parts = [part.strip() for part in str(field or '').split('.') if part.strip()]
    if not parts:
        raise ValueError('field is required.')
    node = data
    for part in parts[:-1]:
        child = node.get(part)
        if not isinstance(child, dict):
            child = {}
            node[part] = child
        node = child
    node[parts[-1]] = value


def update_frontmatter_document(content: str, field: str, value: Any) -> str:
    frontmatter, body = parse_yaml_frontmatter(content)
    if not frontmatter:
        raise ValueError('document must contain YAML frontmatter.')
    set_nested_field(frontmatter, field, value)
    rendered = render_yaml_frontmatter(frontmatter)
    if body and body.strip():
        return f'{rendered}{body}'
    return rendered

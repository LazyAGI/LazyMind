import json
from typing import Any, Dict, List, Optional


def normalize_table_image_map(value: Any) -> List[Dict[str, str]]:
    """Normalize table_image_map to list[dict] format, compatible with legacy dict and JSON string."""
    if not value:
        return []
    if isinstance(value, (bytes, bytearray)):
        value = value.decode('utf-8')
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except (TypeError, ValueError):
            return []
    if isinstance(value, dict):
        return [{'content': str(k), 'image': str(v)} for k, v in value.items()]
    if not isinstance(value, list):
        return []
    result = []
    for item in value:
        if isinstance(item, dict) and item.get('content') and item.get('image'):
            result.append({'content': str(item['content']), 'image': str(item['image'])})
    return result


def merge_table_image_maps(*values: Any) -> List[Dict[str, str]]:
    """Merge multiple table_image_maps, keeping the last entry for duplicate content keys."""
    merged = {}
    for value in values:
        for item in normalize_table_image_map(value):
            merged[item['content']] = item['image']
    return [{'content': k, 'image': v} for k, v in merged.items()]


def serialize_table_image_map(value: Any) -> Optional[str]:
    """Serialize to JSON string to prevent OpenSearch from expanding dict keys as dynamic mapping."""
    normalized = normalize_table_image_map(value)
    return json.dumps(normalized, ensure_ascii=False) if normalized else None

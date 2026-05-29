from typing import Any


def extract_text_from_model_output(model_output: Any) -> str:
    """Normalize string/dict outputs from chat or VLM modules into plain text."""
    if isinstance(model_output, str):
        return model_output.strip()
    if isinstance(model_output, dict):
        for key in ('text', 'content', 'answer'):
            value = model_output.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    return str(model_output).strip()

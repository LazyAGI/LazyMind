"""Model-independent text token approximation."""
import math


def estimate_tokens(text: str) -> int:
    """Fast model-agnostic approximation; deliberately avoids tokenizer dependencies."""
    weight = 0.0
    for char in str(text or ''):
        code = ord(char)
        if char.isspace():
            weight += 0.1
        elif code < 128 and char.isalnum():
            weight += 0.25
        elif code < 128:
            weight += 0.4
        elif 0x3400 <= code <= 0x9FFF or 0xF900 <= code <= 0xFAFF:
            weight += 1.1
        elif 0x3040 <= code <= 0x30FF or 0xAC00 <= code <= 0xD7AF:
            weight += 1.1
        else:
            weight += 1.5
    return math.ceil(weight) if weight else 0

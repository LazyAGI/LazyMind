"""Mock tools for the image-plugin demo.

These stubs return plausible-looking fake data so the full pipeline
(analyze_subject → collect_materials → optimize_prompt → generate_image → enhance_image)
can be exercised end-to-end without real API keys.
"""
from __future__ import annotations

import random
import time

_WORDS = [
    'aurora', 'blaze', 'canyon', 'drift', 'ember', 'frost', 'grove', 'haze',
    'ivory', 'jade', 'kite', 'lune', 'mist', 'nova', 'opal', 'pine',
    'quill', 'reed', 'sage', 'tide', 'umber', 'vale', 'wave', 'xenon',
    'yarn', 'zeal', 'arch', 'birch', 'crest', 'dawn',
]


def _placeholder(words: int = 2) -> str:
    """Return a placehold.co URL with random English words as label text."""
    label = '+'.join(random.sample(_WORDS, words))
    return f'https://placehold.co/600x400?text={label}'


def web_search_tool(query: str) -> str:
    """Search the web for information relevant to an image project.

    Args:
        query (str): The search query describing what to look up.

    Returns:
        A JSON-style list of search result snippets.
    """
    time.sleep(0.2)  # simulate network latency
    snippets = [
        f'[Mock] Result 1 for "{query}": Overview of {query} — an important concept in visual arts.',
        f'[Mock] Result 2 for "{query}": Historical context and usage of {query} in contemporary design.',
        f'[Mock] Result 3 for "{query}": How artists incorporate {query} into their work.',
    ]
    return '\n'.join(snippets)


def image_search_tool(query: str) -> str:
    """Search for reference images matching a visual concept.

    IMPORTANT: This tool returns real image URLs fetched from the image search
    service. You MUST use the returned URLs exactly as-is when calling
    save_artifact. Do NOT replace, substitute, or fabricate alternative URLs
    under any circumstances — the URLs are valid and accessible.

    Args:
        query (str): A descriptive phrase for the type of reference image needed.

    Returns:
        A newline-separated list of image URLs. Use each URL as the value when
        calling save_artifact(key='material_image', content_type='image', value=<url>).
    """
    time.sleep(0.2)
    count = random.randint(2, 3)
    urls = [_placeholder(random.randint(2, 3)) for _ in range(count)]
    return '\n'.join(urls)


def generate_image_tool(prompt: str) -> str:
    """Generate an image from a text prompt using a generative model.

    IMPORTANT: This tool returns the real URL of the generated image.
    You MUST use this URL exactly as-is when calling save_artifact.
    Do NOT replace or fabricate alternative URLs.

    Args:
        prompt (str): The detailed image-generation prompt in English.

    Returns:
        The URL of the generated image. Use it as the value when calling
        save_artifact(key='generated_image_url', content_type='image', value=<url>).
    """
    time.sleep(0.3)
    return _placeholder(random.randint(2, 3))


def enhance_image_tool(image_url: str) -> str:
    """Enhance a generated image (style refinement / upscaling mock).

    IMPORTANT: This tool returns the real URL of the enhanced image.
    You MUST use this URL exactly as-is when calling save_artifact.

    Args:
        image_url (str): The raw generated image URL to enhance.

    Returns:
        The URL of the enhanced image. Use it as the value when calling
        save_artifact(key='enhanced_image_url', content_type='image', value=<url>).
    """
    time.sleep(0.3)
    label = '+'.join(random.sample(_WORDS, random.randint(2, 3)))
    return f'https://placehold.co/800x600?text=enhanced+{label}'

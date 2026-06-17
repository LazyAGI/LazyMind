"""Mock tools for the image-plugin demo.

These stubs return plausible-looking fake data so the full pipeline
(analyze_subject → collect_material → optimize_prompt → generate_image)
can be exercised end-to-end without real API keys.
"""
from __future__ import annotations

import random
import time


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

    Args:
        query (str): A descriptive phrase for the type of reference image needed.

    Returns:
        A newline-separated list of mock image URLs.
    """
    time.sleep(0.2)
    seed = abs(hash(query)) % 1000
    urls = [
        f'https://mock-images.example.com/ref/{seed}_1.jpg',
        f'https://mock-images.example.com/ref/{seed}_2.jpg',
        f'https://mock-images.example.com/ref/{seed}_3.jpg',
    ]
    return '\n'.join(urls)


def generate_image_tool(prompt: str) -> str:
    """Generate an image from a text prompt using a generative model.

    Args:
        prompt (str): The detailed image-generation prompt in English.

    Returns:
        The URL of the generated image.
    """
    time.sleep(0.3)
    seed = abs(hash(prompt)) % 99999
    variation = random.randint(100, 999)
    url = f'https://mock-images.example.com/generated/{seed}_{variation}.png'
    return url

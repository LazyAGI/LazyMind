"""Image plugin tools — pure functions, no side effects."""
from __future__ import annotations

# Placeholder image used when no real image-generation model is available.
# Replace this URL with an actual generation call once a model is integrated.
_PLACEHOLDER_IMAGE_URL = (
    'https://upload.wikimedia.org/wikipedia/commons/thumb/4/47/'
    'PNG_transparency_demonstration_1.png/280px-PNG_transparency_demonstration_1.png'
)


def dalle_generate(prompt: str) -> str:
    """Generate an image from a text prompt and return the image URL.

    Args:
        prompt: The English image generation prompt.

    Returns:
        URL of the generated image.
    """
    # No real image-generation model is available yet.
    # Return a fixed placeholder image so the rest of the plugin flow works end-to-end.
    _ = prompt  # accepted but not used until a real model is wired in
    return _PLACEHOLDER_IMAGE_URL

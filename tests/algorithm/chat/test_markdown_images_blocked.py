from lazymind.chat.service.utils.markdown_images import rewrite_markdown_image_urls


def test_rewrite_strips_blocked_minimax_cdn_when_no_registry_match():
    markdown = (
        '![frame](https://agent-cdn.minimax.io/matrix_agent/tool_output/frame.jpg)'
    )
    rewritten = rewrite_markdown_image_urls(markdown)
    assert 'agent-cdn.minimax.io' not in rewritten
    assert '![frame]' not in rewritten or '/static-files/' in rewritten

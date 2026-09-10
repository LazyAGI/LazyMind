from lazymind.chat.service.component.tool_registry import apply_tool_supersession


def test_declarative_tool_supersession_accepts_mcp_normalized_names():
    def high_level():
        pass

    def vocabulary_review_start():
        pass

    def unrelated():
        pass

    high_level.__supersedes_tools__ = ('vocabulary.review.start',)

    assert apply_tool_supersession([
        vocabulary_review_start, unrelated, high_level,
    ]) == [unrelated, high_level]

"""Memory toolkit exposing durable memory read and edit operations."""
from .memory_editor import memory_editor
from .memory_reader import read_memory


class MemoryToolkit:
    """Read and update durable user preferences and agent working memory.

    Read the current target before editing it. Use read_memory for inspection
    and memory_editor only for durable cross-session information.
    """

    __public_apis__ = ['read_memory', 'memory_editor']

    read_memory = staticmethod(read_memory)
    memory_editor = staticmethod(memory_editor)

"""Chat engine tool package.

Importing this package eagerly loads built-in tool modules so any module-level
registration side effects happen in one consistent place.
"""

from .calculator import calculator
from .kb import KBToolGroup, kb_tmp_search
from .local_fs import LocalFSToolGroup
from .memory_editor import memory_editor
from .memory_reader import read_memory
from .multimodal import image_editor, image_generator, vision_extractor
from .skill_editor import skill_editor
from .vocab_learn import vocab_learn
from .web_search import url_fetch

__all__ = [
    'calculator',
    'image_editor',
    'image_generator',
    'KBToolGroup',
    'kb_tmp_search',
    'LocalFSToolGroup',
    'memory_editor',
    'read_memory',
    'vision_extractor',
    'skill_editor',
    'vocab_learn',
    'url_fetch',
]

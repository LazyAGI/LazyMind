"""Chat engine tool package.

Importing this package eagerly loads built-in tool modules so any module-level
registration side effects happen in one consistent place.
"""

from .calculator import calculator
from .external_db import ExternalDatabaseToolkit
from .kb import KBToolkit, kb_tmp_search
from .local_fs import LocalFileToolkit
from .memory_editor import memory_editor
from .memory_reader import read_memory
from .multimodal import image_editor, image_generator, video_generator, video_to_gif, vision_extractor
from .plugin_chat_tools import create_plugin_draft
from .schedule import build_schedule_toolkit
from .skill_editor import SkillManagementToolkit
from .system_query import list_data_sources
from .vocab_learn import vocab_learn
from .web_search import url_fetch
from .writer import WriterCreateToolkit, WriterRevisionToolkit

__all__ = [
    'build_schedule_toolkit',
    'calculator',
    'create_plugin_draft',
    'ExternalDatabaseToolkit',
    'image_editor',
    'image_generator',
    'video_generator',
    'video_to_gif',
    'KBToolkit',
    'kb_tmp_search',
    'LocalFileToolkit',
    'memory_editor',
    'read_memory',
    'vision_extractor',
    'SkillManagementToolkit',
    'list_data_sources',
    'vocab_learn',
    'url_fetch',
    'WriterCreateToolkit',
    'WriterRevisionToolkit',
]

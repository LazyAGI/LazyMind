from __future__ import annotations

import json
import re
from html import escape
from typing import Any

_TOOL_PREVIEW_TAG = 'tp'
_TOOL_RESULT_PREVIEW_TAG = 'trp'
_TOOL_CALL_TAG = 'tool_call'
_TOOL_RESULT_TAG = 'tool_result'

_REPRESENTATIVE_TOOL_ARGUMENTS: dict[str, str] = {
    'kb_search': 'query',
    'kb_tmp_search': 'query',
    'kb_get_parent_node': 'node_id',
    'kb_get_window_nodes': 'number',
    'kb_keyword_search': 'keyword',
    'calculator': 'expression',
    'WikipediaSearch_search': 'query',
    'GoogleSearch_search': 'query',
    'BingSearch_search': 'query',
    'BochaSearch_search': 'query',
    'url_fetch': 'url',
    'ArxivSearch_search': 'query',
    'memory_editor': 'suggestions.title',
    'vocab_learn': 'suggestions.word <-> suggestions.synonym',
    'vision_extractor': 'url',
    'skill_editor': 'category/name',
    'get_skill': 'name',
    'read_reference': 'rel_path',
    'run_script': 'rel_path',
    'read_file': 'path',
    'list_dir': 'path',
    'search_in_files': 'pattern',
    'make_dir': 'path',
    'write_file': 'path',
    'delete_file': 'path',
    'move_file': 'src',
    'download_file': 'url',
    'FeishuWikiFS_ls': 'path',
    'FeishuWikiFS_info': 'path',
    'FeishuWikiFS_mkdir': 'path',
    'FeishuWikiFS_rm': 'path',
    'FeishuWikiFS_exists': 'path',
    'FeishuWikiFS_read': 'path',
    'FeishuWikiFS_read_file': 'path',
    'FeishuWikiFS_write': 'path',
    'FeishuWikiFS_move': 'path1',
    'FeishuWikiFS_copy': 'path1',
}

_REPRESENTATIVE_TOOL_RESULTS: dict[str, str] = {
    'WikipediaSearch_search': 'title',
    'GoogleSearch_search': 'title',
    'BingSearch_search': 'title',
    'BochaSearch_search': 'title',
    'url_fetch': 'final_url',
    'ArxivSearch_search': 'query',
    'calculator': 'result',
    'vision_extractor': 'description',
    'skill_editor': 'reason',
    'run_script': 'stdout',
    'read_file': 'content',
    'list_dir': 'path',
    'search_in_files': 'status',
    'make_dir': 'path',
    'write_file': 'path',
    'delete_file': 'path',
    'move_file': 'dst',
    'download_file': 'path',
    'FeishuWikiFS_ls': 'path',
    'FeishuWikiFS_info': 'path',
    'FeishuWikiFS_mkdir': 'path',
    'FeishuWikiFS_rm': 'path',
    'FeishuWikiFS_exists': 'path',
    'FeishuWikiFS_read': 'path',
    'FeishuWikiFS_read_file': 'content',
    'FeishuWikiFS_write': 'path',
    'FeishuWikiFS_move': 'path2',
    'FeishuWikiFS_copy': 'path2',
}

_TOOL_CALL_PREVIEW_TEMPLATES: dict[str, str] = {
    'kb_search': 'Checking {value} in the knowledge base for relevant material.',
    'kb_tmp_search': 'Checking attached files for material related to {value}.',
    'kb_get_parent_node': 'Loading surrounding context for {value} before continuing now.',
    'kb_get_window_nodes': 'Expanding nearby related segments around {value} for review.',
    'kb_keyword_search': 'Searching target documents with {value} as the keyword.',
    'calculator': 'Evaluating the expression {value}.',
    'WikipediaSearch_search': 'Searching Wikipedia for {value}.',
    'GoogleSearch_search': 'Searching Google for {value}.',
    'BingSearch_search': 'Searching Bing for {value}.',
    'BochaSearch_search': 'Searching Bocha for {value}.',
    'url_fetch': 'Reading page content from {value}.',
    'ArxivSearch_search': 'Searching arXiv papers for {value}.',
    'vision_extractor': 'Extracting information from the image.',
    'memory_editor': 'Saving {value} as useful long term memory now.',
    'vocab_learn': 'Updating vocabulary entries for {value} now.',
    'skill_editor': 'Updating reusable skill notes related to {value} now.',
    'get_skill': 'Opening skill details for {value} before continuing now.',
    'read_reference': 'Reading skill reference material from {value} for review.',
    'run_script': 'Running the selected skill helper script at {value} now.',
    'read_file': 'Reading file content from {value} for review now.',
    'list_dir': 'Listing folder contents from {value} for review now.',
    'search_in_files': 'Searching project files for matches to {value} now.',
    'make_dir': 'Preparing folder {value} for the requested use now.',
    'write_file': 'Writing requested content into file {value} now for update.',
    'delete_file': 'Preparing file {value} for the requested deletion now.',
    'move_file': 'Preparing file move operation starting from {value} now.',
    'download_file': 'Downloading requested file from source {value} now for use.',
    'FeishuWikiFS_ls': 'Listing Feishu folder contents at {value}.',
    'FeishuWikiFS_info': 'Fetching Feishu file info for {value}.',
    'FeishuWikiFS_mkdir': 'Creating Feishu folder at {value}.',
    'FeishuWikiFS_rm': 'Deleting Feishu file or folder at {value}.',
    'FeishuWikiFS_exists': 'Checking whether {value} exists in Feishu.',
    'FeishuWikiFS_read': 'Reading Feishu document content from {value}.',
    'FeishuWikiFS_read_file': 'Reading Feishu file content from {value}.',
    'FeishuWikiFS_write': 'Writing content to Feishu file at {value}.',
    'FeishuWikiFS_move': 'Moving Feishu file from {value} to the target path.',
    'FeishuWikiFS_copy': 'Copying Feishu file from {value} to the target path.',
}
_TOOL_CALL_FALLBACK_TEMPLATE = 'Calling a tool to handle the request.'

_ZH_TOOL_CALL_PREVIEW_TEMPLATES: dict[str, str] = {
    'kb_search': '正在知识库中检索与 {value} 相关的知识。',
    'kb_tmp_search': '正在附件中检索与 {value} 相关的内容。',
    'kb_get_parent_node': '正在加载 {value} 的相关上下文。',
    'kb_get_window_nodes': '正在扩展 {value} 附近的相关片段。',
    'kb_keyword_search': '正在目标文档中搜索关键词 {value}。',
    'calculator': '正在计算表达式 {value}。',
    'WikipediaSearch_search': '正在维基百科中搜索 {value}。',
    'GoogleSearch_search': '正在 Google 中搜索 {value}。',
    'BingSearch_search': '正在 Bing 中搜索 {value}。',
    'BochaSearch_search': '正在博查中搜索 {value}。',
    'url_fetch': '正在读取网页 {value} 。',
    'ArxivSearch_search': '正在 arXiv 中搜索论文 {value}。',
    'vision_extractor': '正在提取图像信息。',
    'memory_editor': '正在将 {value} 保存为长期记忆。',
    'vocab_learn': '正在更新与 {value} 相关的词汇表。',
    'skill_editor': '正在更新与 {value} 相关的技能。',
    'get_skill': '正在打开 {value} 的技能详情。',
    'read_reference': '正在读取 {value} 技能的参考资料。',
    'run_script': '正在运行技能 {value} 的预定义脚本。',
    'read_file': '正在读取文件 {value}。',
    'list_dir': '正在列出文件夹 {value} 的内容。',
    'search_in_files': '正在项目文件中搜索 {value} 的相关内容。',
    'make_dir': '正在创建文件夹 {value}。',
    'write_file': '正在向文件 {value} 中写入内容。',
    'delete_file': '正在准备删除文件 {value}。',
    'move_file': '正在准备移动文件 {value}。',
    'download_file': '正在从 {value} 下载文件。',
    'FeishuWikiFS_ls': '正在列出飞书文件夹 {value} 的内容。',
    'FeishuWikiFS_info': '正在获取飞书文件 {value} 的信息。',
    'FeishuWikiFS_mkdir': '正在飞书中创建文件夹 {value}。',
    'FeishuWikiFS_rm': '正在删除飞书文件或文件夹 {value}。',
    'FeishuWikiFS_exists': '正在检查 {value} 是否存在于飞书中。',
    'FeishuWikiFS_read': '正在读取飞书文档 {value} 的内容。',
    'FeishuWikiFS_read_file': '正在读取飞书文件 {value} 的内容。',
    'FeishuWikiFS_write': '正在向飞书文件 {value} 写入内容。',
    'FeishuWikiFS_move': '正在将飞书文件从 {value} 移动到目标路径。',
    'FeishuWikiFS_copy': '正在将飞书文件从 {value} 复制到目标路径。',
}
_ZH_TOOL_CALL_FALLBACK_TEMPLATE = '正在调用工具处理请求...'

_TOOL_RESULT_PREVIEW_TEMPLATES: dict[str, str] = {
    'kb_search': 'Knowledge base results for {value} are ready now.',
    'kb_tmp_search': 'Attached file results for {value} are ready now.',
    'kb_get_parent_node': 'Surrounding context for {value} was loaded successfully now.',
    'kb_get_window_nodes': 'Nearby related segments around {value} were expanded successfully.',
    'kb_keyword_search': 'Document results for keyword {value} were found successfully.',
    'calculator': 'Expression was evaluated successfully, result is {value}',
    'WikipediaSearch_search': 'Wikipedia results for {value} are ready now.',
    'GoogleSearch_search': 'Google results for {value} are ready now.',
    'BingSearch_search': 'Bing results for {value} are ready now.',
    'BochaSearch_search': 'Bocha results for {value} are ready now.',
    'url_fetch': 'Page content from {value} was loaded successfully.',
    'ArxivSearch_search': 'arXiv results for {value} are ready now.',
    'vision_extractor': 'Image information has been extracted.',
    'memory_editor': 'Long term memory for {value} was saved successfully.',
    'vocab_learn': 'Vocabulary entries for {value} were updated successfully.',
    'skill_editor': 'Reusable skill notes for {value} were updated successfully.',
    'get_skill': 'Skill details for {value} were loaded successfully now.',
    'read_reference': 'Skill reference material from {value} was loaded successfully.',
    'run_script': 'Skill helper script at {value} finished running successfully.',
    'read_file': 'File content from {value} was loaded successfully now.',
    'list_dir': 'Folder contents from {value} were retrieved successfully now.',
    'search_in_files': 'Project file matches for {value} were found successfully.',
    'make_dir': 'Folder {value} is ready for the requested use.',
    'write_file': 'Requested content was written into {value} successfully.',
    'delete_file': 'Requested deletion for file {value} completed successfully now.',
    'move_file': 'Requested file move from {value} completed successfully now.',
    'download_file': 'Requested file from {value} was downloaded successfully now.',
    'FeishuWikiFS_ls': 'Feishu folder contents at {value} were listed successfully.',
    'FeishuWikiFS_info': 'Feishu file info for {value} was retrieved successfully.',
    'FeishuWikiFS_mkdir': 'Feishu folder at {value} was created successfully.',
    'FeishuWikiFS_rm': 'Feishu file or folder at {value} was deleted successfully.',
    'FeishuWikiFS_exists': 'Existence check for {value} in Feishu completed successfully.',
    'FeishuWikiFS_read': 'Feishu document content from {value} was loaded successfully.',
    'FeishuWikiFS_read_file': 'Feishu file content from {value} was loaded successfully.',
    'FeishuWikiFS_write': 'Content was written to Feishu file at {value} successfully.',
    'FeishuWikiFS_move': 'Feishu file was moved from {value} to the target path successfully.',
    'FeishuWikiFS_copy': 'Feishu file was copied from {value} to the target path successfully.',
}

_ZH_TOOL_RESULT_PREVIEW_TEMPLATES: dict[str, str] = {
    'kb_search': '已查询到 {value} 的知识库结果。',
    'kb_tmp_search': '已查询到 {value} 的附件检索结果。',
    'kb_get_parent_node': '已成功加载 {value} 的相关上下文。',
    'kb_get_window_nodes': '已成功扩展 {value} 附近的相关片段。',
    'kb_keyword_search': '已找到关键词 {value} 的文档结果。',
    'calculator': '已计算完成，结果为 {value}',
    'WikipediaSearch_search': '已找到 {value} 的维基百科结果。',
    'GoogleSearch_search': '已找到 {value} 的 Google 结果。',
    'BingSearch_search': '已找到 {value} 的 Bing 结果。',
    'BochaSearch_search': '已找到 {value} 的博查结果。',
    'url_fetch': '已成功加载 {value} 的网页内容。',
    'ArxivSearch_search': '已找到 {value} 的 arXiv 结果。',
    'vision_extractor': '已成功提取图像信息。',
    'memory_editor': '已成功保存 {value} 的长期记忆。',
    'vocab_learn': '已成功更新 {value} 的词汇表。',
    'skill_editor': '已成功更新 {value} 的技能。',
    'get_skill': '已成功加载 {value} 的技能详情。',
    'read_reference': '已成功加载 {value} 技能的参考资料。',
    'run_script': '技能 {value} 的预定义脚本已成功运行。',
    'read_file': '已成功加载文件 {value} 的内容。',
    'list_dir': '已成功获取文件夹 {value} 的内容。',
    'search_in_files': '已找到项目文件中与 {value} 匹配的内容。',
    'make_dir': '文件夹 {value} 已准备好。',
    'write_file': '已成功向 {value} 写入内容。',
    'delete_file': '已成功完成文件 {value} 的删除操作。',
    'move_file': '已成功完成从 {value} 开始的文件移动操作。',
    'download_file': '已成功下载来自 {value} 的文件。',
    'FeishuWikiFS_ls': '已成功列出飞书文件夹 {value} 的内容。',
    'FeishuWikiFS_info': '已成功获取飞书文件 {value} 的信息。',
    'FeishuWikiFS_mkdir': '已成功在飞书中创建文件夹 {value}。',
    'FeishuWikiFS_rm': '已成功删除飞书文件或文件夹 {value}。',
    'FeishuWikiFS_exists': '已完成对飞书中 {value} 的存在性检查。',
    'FeishuWikiFS_read': '已成功读取飞书文档 {value} 的内容。',
    'FeishuWikiFS_read_file': '已成功读取飞书文件 {value} 的内容。',
    'FeishuWikiFS_write': '已成功向飞书文件 {value} 写入内容。',
    'FeishuWikiFS_move': '已成功将飞书文件从 {value} 移动到目标路径。',
    'FeishuWikiFS_copy': '已成功将飞书文件从 {value} 复制到目标路径。',
}

_TOOL_RESULT_FAILURE_TEMPLATES: dict[str, str] = {
    'kb_search': 'Knowledge base results for {value} could not be found.',
    'kb_tmp_search': 'Attached file results for {value} could not be found.',
    'kb_get_parent_node': 'Surrounding context for {value} could not be loaded.',
    'kb_get_window_nodes': 'Nearby related segments around {value} could not be expanded.',
    'kb_keyword_search': 'Document results for keyword {value} could not be found.',
    'calculator': 'Expression {value} could not be evaluated.',
    'WikipediaSearch_search': 'Wikipedia results for {value} could not be retrieved.',
    'GoogleSearch_search': 'Google results for {value} could not be retrieved.',
    'BingSearch_search': 'Bing results for {value} could not be retrieved.',
    'BochaSearch_search': 'Bocha results for {value} could not be retrieved.',
    'url_fetch': 'Page content from {value} could not be loaded.',
    'ArxivSearch_search': 'arXiv results for {value} could not be retrieved.',
    'vision_extractor': 'Vision extraction for {value} could not be completed.',
    'memory_editor': 'Long term memory for {value} could not be saved.',
    'vocab_learn': 'Vocabulary entries for {value} could not be updated.',
    'skill_editor': 'Reusable skill notes for {value} could not be updated.',
    'get_skill': 'Skill details for {value} could not be loaded.',
    'read_reference': 'Skill reference material from {value} could not be read.',
    'run_script': 'Skill helper script at {value} did not finish.',
    'read_file': 'File content from {value} could not be read.',
    'list_dir': 'Folder contents from {value} could not be listed.',
    'search_in_files': 'Project file search for {value} could not finish.',
    'make_dir': 'Folder {value} could not be prepared for use.',
    'write_file': 'Requested content could not be written into {value} now.',
    'delete_file': 'Requested deletion for file {value} could not complete.',
    'move_file': 'Requested file move from {value} could not complete.',
    'download_file': 'Requested file from {value} could not be downloaded.',
    'FeishuWikiFS_ls': 'Feishu folder contents at {value} could not be listed.',
    'FeishuWikiFS_info': 'Feishu file info for {value} could not be retrieved.',
    'FeishuWikiFS_mkdir': 'Feishu folder at {value} could not be created.',
    'FeishuWikiFS_rm': 'Feishu file or folder at {value} could not be deleted.',
    'FeishuWikiFS_exists': 'Existence check for {value} in Feishu could not be completed.',
    'FeishuWikiFS_read': 'Feishu document content from {value} could not be loaded.',
    'FeishuWikiFS_read_file': 'Feishu file content from {value} could not be loaded.',
    'FeishuWikiFS_write': 'Content could not be written to Feishu file at {value}.',
    'FeishuWikiFS_move': 'Feishu file could not be moved from {value} to the target path.',
    'FeishuWikiFS_copy': 'Feishu file could not be copied from {value} to the target path.',
}

_ZH_TOOL_RESULT_FAILURE_TEMPLATES: dict[str, str] = {
    'kb_search': '未能找到 {value} 的知识库结果。',
    'kb_tmp_search': '未能找到 {value} 的附件检索结果。',
    'kb_get_parent_node': '未能加载 {value} 的相关上下文。',
    'kb_get_window_nodes': '未能扩展 {value} 附近的相关片段。',
    'kb_keyword_search': '未能找到关键词 {value} 的文档结果。',
    'calculator': '未能计算表达式 {value}。',
    'WikipediaSearch_search': '未能获取 {value} 的维基百科结果。',
    'GoogleSearch_search': '未能获取 {value} 的 Google 结果。',
    'BingSearch_search': '未能获取 {value} 的 Bing 结果。',
    'BochaSearch_search': '未能获取 {value} 的博查结果。',
    'url_fetch': '未能加载网页 {value} 的内容。',
    'ArxivSearch_search': '未能获取 {value} 的 arXiv 结果。',
    'vision_extractor': '未能完成 {value} 的图像信息提取。',
    'memory_editor': '未能保存 {value} 的长期记忆。',
    'vocab_learn': '未能更新 {value} 的词汇表。',
    'skill_editor': '未能更新 {value} 的技能。',
    'get_skill': '未能加载 {value} 的技能详情。',
    'read_reference': '未能读取 {value} 技能参考资料。',
    'run_script': '技能 {value} 的预定义脚本未能运行完成。',
    'read_file': '未能读取文件 {value} 的内容。',
    'list_dir': '未能列出文件夹 {value} 的内容。',
    'search_in_files': '未能完成项目文件中与 {value} 相关的搜索。',
    'make_dir': '未能创建文件夹 {value}。',
    'write_file': '未能向 {value} 写入内容。',
    'delete_file': '未能完成文件 {value} 的删除操作。',
    'move_file': '未能完成从 {value} 开始的文件移动操作。',
    'download_file': '未能下载来自 {value} 的文件。',
    'FeishuWikiFS_ls': '未能列出飞书文件夹 {value} 的内容。',
    'FeishuWikiFS_info': '未能获取飞书文件 {value} 的信息。',
    'FeishuWikiFS_mkdir': '未能在飞书中创建文件夹 {value}。',
    'FeishuWikiFS_rm': '未能删除飞书文件或文件夹 {value}。',
    'FeishuWikiFS_exists': '未能完成对飞书中 {value} 的存在性检查。',
    'FeishuWikiFS_read': '未能读取飞书文档 {value} 的内容。',
    'FeishuWikiFS_read_file': '未能读取飞书文件 {value} 的内容。',
    'FeishuWikiFS_write': '未能向飞书文件 {value} 写入内容。',
    'FeishuWikiFS_move': '未能将飞书文件从 {value} 移动到目标路径。',
    'FeishuWikiFS_copy': '未能将飞书文件从 {value} 复制到目标路径。',
}

_TOOL_RESULT_APPROVAL_TEMPLATES: dict[str, str] = {
    'delete_file': 'Please review the confirmation note "{value}" before deleting this file.',
    'move_file': 'Please review the confirmation note "{value}" before moving this file.',
    'write_file': 'Please review the confirmation note "{value}" before writing this file.',
    'download_file': 'Please review the confirmation note "{value}" before downloading this file.',
    'FeishuWikiFS_rm': 'Please review the confirmation note "{value}" before deleting this Feishu file.',
    'FeishuWikiFS_move': 'Please review the confirmation note "{value}" before moving this Feishu file.',
    'FeishuWikiFS_write': 'Please review the confirmation note "{value}" before writing this Feishu file.',
}

_ZH_TOOL_RESULT_APPROVAL_TEMPLATES: dict[str, str] = {
    'delete_file': '删除这个文件前，请先确认提示“{value}”。',
    'move_file': '移动这个文件前，请先确认提示“{value}”。',
    'write_file': '写入这个文件前，请先确认提示“{value}”。',
    'download_file': '下载这个文件前，请先确认提示“{value}”。',
    'FeishuWikiFS_rm': '删除这个飞书文件前，请先确认提示“{value}”。',
    'FeishuWikiFS_move': '移动这个飞书文件前，请先确认提示“{value}”。',
    'FeishuWikiFS_write': '写入这个飞书文件前，请先确认提示“{value}”。',
}

_TOOL_RESULT_FALLBACK_TEMPLATE = 'Tool processing has finished.'
_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE = 'Tool processing could not be completed.'
_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE = 'This operation needs confirmation before continuing.'
_TOOL_RESULT_INACTIVE_FALLBACK_TEMPLATE = 'Tool inactive.'
_ZH_TOOL_RESULT_FALLBACK_TEMPLATE = '工具处理已完成。'
_ZH_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE = '工具处理未能完成。'
_ZH_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE = '此操作需要确认后才能继续。'
_ZH_TOOL_RESULT_INACTIVE_FALLBACK_TEMPLATE = '工具未激活。'

_KB_EMPTY_RESULT_MESSAGES: dict[str, dict[str, str]] = {
    'kb_search': {
        'en': 'Knowledge base search finished with no matching results',
        'zh': '知识库搜索已完成，但没有找到匹配结果',
    },
    'kb_get_parent_node': {
        'en': 'No parent context was found for the requested node',
        'zh': '未找到请求节点的上级上下文',
    },
    'kb_get_window_nodes': {
        'en': 'No nearby knowledge base segments were found',
        'zh': '未找到附近的知识库片段',
    },
    'kb_keyword_search': {
        'en': 'Keyword search finished with no matching document segments',
        'zh': '关键词搜索已完成，但没有找到匹配的文档片段',
    },
}

_FALLBACK_REPRESENTATIVE_RESULT_KEYS = (
    'result',
    'content',
    'text',
    'reason',
    'message',
    'stdout',
    'stderr',
    'status',
    'path',
)

_FALLBACK_REPRESENTATIVE_ARGUMENT_KEYS = (
    'query',
    'keyword',
    'keywords',
    'url',
    'urls',
    'path',
    'file',
    'filename',
    'rel_path',
    'name',
    'title',
    'topic',
    'pattern',
    'target',
    'node_id',
    'id',
    'src',
    'dst',
    'text',
    'content',
)

_LOW_SIGNAL_ARGUMENT_KEYS = {
    'include_content',
    'include_metadata',
    'include_raw',
    'max_results',
    'limit',
    'top_k',
    'k',
    'page',
    'page_size',
    'offset',
}

_MAX_REPRESENTATIVE_RESULT_LENGTH = 200
_MAX_TOOL_RESULT_PREVIEW_LENGTH = 50

_ZH_PREVIEW_RE = re.compile('[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]')
_TOOL_NOT_AVAILABLE_RE = re.compile(
    r'Tool \[[^\]]+\] is not available\. Please choose from the available tools\.',
    re.IGNORECASE,
)


def _resolve_tool_key(tool_name: str, mapping: dict[str, Any]) -> Any:
    """Look up *tool_name* in *mapping*, falling back to suffix match for
    class-registered tools prefixed like ``KBToolGroup_kb_search``."""
    if not tool_name or not mapping:
        return None
    if tool_name in mapping:
        return mapping[tool_name]
    parts = tool_name.split('_')
    for i in range(1, len(parts)):
        suffix = '_'.join(parts[i:])
        if suffix in mapping:
            return mapping[suffix]
    return None


def _tool_name_is(tool_name: str, base_name: str) -> bool:
    """Return True when *tool_name* equals *base_name* or is a prefixed
    variant like ``GroupName_<base_name>``."""
    if tool_name == base_name:
        return True
    return tool_name.endswith('_' + base_name)


def _tool_name_starts(tool_name: str, prefix: str) -> bool:
    """Like ``str.startswith`` but works through group prefixes."""
    if tool_name.startswith(prefix):
        return True
    parts = tool_name.split('_')
    for i in range(1, len(parts)):
        if '_'.join(parts[i:]).startswith(prefix):
            return True
    return False


def _preview_language(value: Any) -> str:
    text = '' if value is None else str(value)
    return 'zh' if _ZH_PREVIEW_RE.search(text) else 'en'


def _language_templates(
    language: str,
    en_templates: dict[str, str],
    zh_templates: dict[str, str],
) -> dict[str, str]:
    return zh_templates if language == 'zh' else en_templates


def _language_fallback(language: str, en_fallback: str, zh_fallback: str) -> str:
    return zh_fallback if language == 'zh' else en_fallback


def _representative_tool_argument(tool_name: str, arguments: Any) -> Any:
    expression = _resolve_tool_key(tool_name, _REPRESENTATIVE_TOOL_ARGUMENTS)
    if not isinstance(arguments, dict):
        return arguments
    if expression:
        value = _representative_expression_value(arguments, expression)
        if _is_meaningful_preview_value(value):
            return value
    return _representative_mapping_value(arguments, _FALLBACK_REPRESENTATIVE_ARGUMENT_KEYS)


def _truncate_representative_result(value: Any) -> str:
    if value is None:
        text = ''
    elif isinstance(value, (dict, list)):
        text = json.dumps(value, ensure_ascii=False, separators=(',', ':'))
    else:
        text = str(value)
    if len(text) <= _MAX_REPRESENTATIVE_RESULT_LENGTH:
        return text
    return f'{text[:_MAX_REPRESENTATIVE_RESULT_LENGTH]}...'


def _is_meaningful_preview_value(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, str):
        return bool(value.strip())
    if isinstance(value, (list, tuple, set, dict)):
        return bool(value)
    if isinstance(value, bool):
        return False
    return True


def _representative_mapping_value(mapping: dict[str, Any], preferred_keys: tuple[str, ...]) -> Any:
    for key in preferred_keys:
        value = mapping.get(key)
        if _is_meaningful_preview_value(value):
            return value
    for key, value in mapping.items():
        if key in _LOW_SIGNAL_ARGUMENT_KEYS:
            continue
        if _is_meaningful_preview_value(value):
            return value
    return ''


def _resolve_representative_path(value: Any, path: str) -> Any:
    if not path:
        return value
    current = value
    parts = path.split('.')
    for index, part in enumerate(parts):
        if isinstance(current, list):
            remaining_path = '.'.join(parts[index:])
            return [
                resolved for item in current
                if _is_meaningful_preview_value(
                    resolved := _resolve_representative_path(item, remaining_path)
                )
            ]
        if not isinstance(current, dict):
            return None
        current = current.get(part)
    return current


def _representative_expression_value(arguments: dict[str, Any], expression: str) -> Any:
    def expression_part_value(part: str) -> Any:
        value = _resolve_representative_path(arguments, part)
        if _is_meaningful_preview_value(value) or '.' not in part:
            return value
        head, leaf = part.split('.', 1)
        return (
            _resolve_representative_path(arguments, leaf)
            or _resolve_representative_path(arguments, head)
        )

    for separator in (' <-> ', '/'):
        if separator not in expression:
            continue
        parts = [part.strip() for part in expression.split(separator)]
        values = [expression_part_value(part) for part in parts]
        if any(isinstance(value, list) for value in values):
            max_count = max((len(value) for value in values if isinstance(value, list)), default=0)
            previews = []
            for index in range(min(max_count, 2)):
                item_parts = [
                    _tool_preview_value(value[index] if isinstance(value, list) and index < len(value) else value)
                    for value in values
                ]
                item_parts = [part for part in item_parts if part]
                if item_parts:
                    previews.append(separator.join(item_parts))
            if previews:
                text = ', '.join(previews)
                if max_count > 2:
                    return f'{text} and {max_count - 2} more'
                return text
        item_parts = [_tool_preview_value(value) for value in values]
        item_parts = [part for part in item_parts if part]
        if item_parts:
            return separator.join(item_parts)
    return _resolve_representative_path(arguments, expression)


def _friendly_preview_text(value: Any) -> str:
    if value is None:
        return ''
    if isinstance(value, str):
        return value
    if isinstance(value, bool):
        return ''
    if isinstance(value, dict):
        representative = _representative_mapping_value(
            value,
            _FALLBACK_REPRESENTATIVE_ARGUMENT_KEYS + _FALLBACK_REPRESENTATIVE_RESULT_KEYS,
        )
        if representative is value or not _is_meaningful_preview_value(representative):
            return 'the selected options'
        return _friendly_preview_text(representative)
    if isinstance(value, (list, tuple, set)):
        items = list(value)
        if not items:
            return ''
        friendly_items = [
            _friendly_preview_text(item)
            for item in items[:2]
            if _is_meaningful_preview_value(item)
        ]
        friendly_items = [item for item in friendly_items if item]
        if friendly_items:
            preview = ', '.join(friendly_items)
            if len(items) > 2:
                return f'{preview} and {len(items) - 2} more'
            return preview
        return f'{len(items)} items'
    return str(value)


def _representative_tool_result(tool_name: str, result: Any) -> Any:
    if isinstance(result, dict):
        payload = result.get('result') if isinstance(result.get('result'), dict) else result
        key = _resolve_tool_key(tool_name, _REPRESENTATIVE_TOOL_RESULTS)
        if key and payload.get(key) is not None:
            return payload.get(key)
        for fallback_key in _FALLBACK_REPRESENTATIVE_RESULT_KEYS:
            if payload.get(fallback_key) is not None:
                return payload.get(fallback_key)
        if payload:
            first_key = next(iter(payload))
            return payload.get(first_key)
        return ''
    if isinstance(result, list):
        return result
    return result


def _tool_preview_value(value: Any) -> str:
    text = _truncate_representative_result(_friendly_preview_text(value))
    return text.replace('\n', ' ').strip()


def _tool_call_preview_value(tool_name: str, arguments: Any, language: str = 'en') -> str:
    preview = _tool_preview_value(_representative_tool_argument(tool_name, arguments))
    if _tool_name_is(tool_name, 'memory_editor') and not preview:
        return '待保存内容' if language == 'zh' else 'memory update'
    return preview


def _truncate_tool_result_preview(value: Any) -> str:
    text = _tool_preview_value(value)
    if len(text) <= _MAX_TOOL_RESULT_PREVIEW_LENGTH:
        return text
    return f'{text[:_MAX_TOOL_RESULT_PREVIEW_LENGTH]}...'


def _tool_result_status(result: Any) -> str:
    if isinstance(result, dict):
        success = result.get('success')
        if success is False:
            return 'failed'
        payload = result.get('result') if isinstance(result.get('result'), dict) else result
        status = str(payload.get('status') or '').strip().lower()
        if status == 'needs_approval':
            return 'needs_approval'
        if status in ('error', 'missing', 'failed', 'fail'):
            return 'failed'
    elif isinstance(result, str):
        text = result.strip().lower()
        if _TOOL_NOT_AVAILABLE_RE.search(result):
            return 'inactive'
        if any(marker in text for marker in ('error', 'failed', 'parameters error')):
            return 'failed'
    return 'ok'


def _tool_result_failure_detail(result: Any) -> str:
    if isinstance(result, dict):
        error = result.get('error')
        if isinstance(error, dict):
            for key in ('reason', 'detail', 'type'):
                value = error.get(key)
                if value:
                    return _truncate_tool_result_preview(value)
        for key in ('reason', 'error', 'message', 'path', 'status'):
            value = result.get(key)
            if value:
                return _truncate_tool_result_preview(value)
    return _truncate_tool_result_preview(result)


def _ensure_trailing_newline(text: str) -> str:
    return text if text.endswith('\n') else f'{text}\n'


def _render_preview_template(
    tool_name: str,
    value: str,
    template_map: dict[str, str],
    fallback_template: str,
) -> str:
    template = _resolve_tool_key(tool_name, template_map) or fallback_template
    if '{value}' not in template:
        return _ensure_trailing_newline(template)
    preview_value = value or 'the current item'
    return _ensure_trailing_newline(template.format(value=f'**{preview_value}**'))


def _tool_call_preview(tool_name: str, preview_value: str, language: str = 'en') -> str:
    return _render_preview_template(
        tool_name,
        preview_value,
        _language_templates(language, _TOOL_CALL_PREVIEW_TEMPLATES, _ZH_TOOL_CALL_PREVIEW_TEMPLATES),
        _language_fallback(language, _TOOL_CALL_FALLBACK_TEMPLATE, _ZH_TOOL_CALL_FALLBACK_TEMPLATE),
    )


def _tool_result_preview_display_value(tool_name: str, result: Any, value: str = '') -> str:
    status = _tool_result_status(result)
    if (
        _tool_name_is(tool_name, 'calculator')
        and status == 'ok'
        and isinstance(result, dict)
        and result.get('result')
    ):
        return _truncate_tool_result_preview(result.get('result'))
    return value or _truncate_tool_result_preview(_representative_tool_result(tool_name, result))


def _tool_result_preview(tool_name: str, result: Any, value: str = '', language: str = 'en') -> str:
    status = _tool_result_status(result)
    display_value = _tool_result_preview_display_value(tool_name, result, value)
    if status == 'inactive':
        return _ensure_trailing_newline(
            _language_fallback(
                language,
                _TOOL_RESULT_INACTIVE_FALLBACK_TEMPLATE,
                _ZH_TOOL_RESULT_INACTIVE_FALLBACK_TEMPLATE,
            )
        )
    if status == 'needs_approval':
        return _render_preview_template(
            tool_name,
            value or _tool_result_failure_detail(result),
            _language_templates(language, _TOOL_RESULT_APPROVAL_TEMPLATES, _ZH_TOOL_RESULT_APPROVAL_TEMPLATES),
            _language_fallback(
                language,
                _TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE,
                _ZH_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE,
            ),
        )
    if status == 'failed':
        return _render_preview_template(
            tool_name,
            display_value or _tool_result_failure_detail(result),
            _language_templates(language, _TOOL_RESULT_FAILURE_TEMPLATES, _ZH_TOOL_RESULT_FAILURE_TEMPLATES),
            _language_fallback(
                language,
                _TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE,
                _ZH_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE,
            ),
        )
    payload = result.get('result') if isinstance(result, dict) and isinstance(result.get('result'), dict) else result
    if isinstance(payload, dict) and payload.get('total') == 0 and _tool_name_starts(tool_name, 'kb_'):
        msg = _resolve_tool_key(tool_name, _KB_EMPTY_RESULT_MESSAGES)
        if msg:
            return _ensure_trailing_newline(msg.get(language) or msg.get('en', ''))
    return _render_preview_template(
        tool_name,
        display_value,
        _language_templates(language, _TOOL_RESULT_PREVIEW_TEMPLATES, _ZH_TOOL_RESULT_PREVIEW_TEMPLATES),
        _language_fallback(language, _TOOL_RESULT_FALLBACK_TEMPLATE, _ZH_TOOL_RESULT_FALLBACK_TEMPLATE),
    )


def _tool_call_frame_text(tool_call: dict[str, Any], language: str = 'en') -> tuple[str, str]:
    function = tool_call.get('function') or {}
    tool_call_id = str(tool_call.get('id') or '')
    tool_name = str(function.get('name', ''))
    raw_args = function.get('arguments', {})
    if isinstance(raw_args, str):
        try:
            arguments = json.loads(raw_args)
        except json.JSONDecodeError:
            arguments = raw_args
    else:
        arguments = raw_args
    preview_value = _tool_call_preview_value(tool_name, arguments, language)
    payload = {
        'id': tool_call_id,
        'name': tool_name,
        'arguments': arguments if isinstance(arguments, dict) else {},
    }
    preview = _tool_call_preview(tool_name, preview_value, language)
    text = (
        f'<{_TOOL_PREVIEW_TAG} id="{escape(tool_call_id, quote=True)}">{preview}</{_TOOL_PREVIEW_TAG}>'
        f'<{_TOOL_CALL_TAG}>{json.dumps(payload, ensure_ascii=False, separators=(",", ":"))}</{_TOOL_CALL_TAG}>'
    )
    return text, preview_value if tool_call_id else ''


def _tool_result_frame_text(tool_result: dict[str, Any], language: str = 'en', preview_value: str = '') -> str:
    tool_call_id = str(tool_result.get('id') or '')
    tool_name = str(tool_result.get('name', ''))
    result = tool_result.get('result')
    payload = {
        'id': tool_call_id,
        'name': tool_name,
        'result': result,
    }
    preview = _tool_result_preview(tool_name, result, preview_value, language)
    return (
        f'<{_TOOL_RESULT_PREVIEW_TAG} id="{escape(tool_call_id, quote=True)}">{preview}</{_TOOL_RESULT_PREVIEW_TAG}>'
        f'<{_TOOL_RESULT_TAG}>{json.dumps(payload, ensure_ascii=False, separators=(",", ":"))}</{_TOOL_RESULT_TAG}>'
    )

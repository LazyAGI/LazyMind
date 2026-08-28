from __future__ import annotations

from typing import Any

# A tool selector and its localized state templates live together so additions
# and renames cannot silently update only one of several parallel maps.
TOOL_RENDER_PROFILES: dict[str, dict[str, Any]] = (
{'kb_search': {'argument': 'query',  # noqa: E122
               'call': {'en': 'Using kb_search for {value}.',
                        'zh': '正在用 kb_search 检索 {value}。'},
               'success': {'en': 'kb_search completed with '
                                 '{result.total} relevant items.',
                           'zh': 'kb_search 完成，共找到 {result.total} 条相关内容。'},
               'failure': {'en': 'kb_search could not find results for {value}.',
                           'zh': 'kb_search 未能找到 {value} 的结果。'}},
 'kb_tmp_search': {'argument': 'semantic_query',
                   'call': {'en': 'Using kb_tmp_search for {value}.',
                            'zh': '正在用 kb_tmp_search 检索 {value}。'},
                   'success': {'en': 'kb_tmp_search completed with '
                                     '{result.total} relevant items.',
                               'zh': 'kb_tmp_search 完成，共找到 {result.total} 条相关内容。'},
                   'failure': {'en': 'kb_tmp_search could not find results for {value}.',
                               'zh': 'kb_tmp_search 未能找到 {value} 的结果。'}},
 'parse_uploaded_files': {'argument': 'files',
                          'call': {'en': 'Parsing uploaded documents: {value}.',
                                   'zh': '正在解析上传文档：{value}。'},
                          'success': {'en': 'Finished parsing {result.total} uploaded document(s).',
                                      'zh': '已完成 {result.total} 个上传文档的解析。'},
                          'failure': {'en': 'Uploaded documents could not be parsed.',
                                      'zh': '未能完成上传文档解析。'}},
 'kb_get_parent_node': {'argument': 'node_id',
                        'call': {'en': 'Loading surrounding context for {value} '
                                       'before continuing now.',
                                 'zh': '正在加载 {value} 的相关上下文。'},
                        'success': {'en': 'Loaded {result.total} parent context '
                                          'items.',
                                    'zh': '已加载 {result.total} 条上级上下文。'},
                        'failure': {'en': 'Surrounding context for {value} could '
                                          'not be loaded.',
                                    'zh': '未能加载 {value} 的相关上下文。'}},
 'kb_get_window_nodes': {'argument': 'node_id',
                         'call': {'en': 'Expanding nearby related segments around '
                                        '{value} for review.',
                                  'zh': '正在扩展 {value} 附近的相关片段。'},
                         'success': {'en': 'Loaded {result.total} nearby knowledge '
                                           'base segments.',
                                     'zh': '已加载 {result.total} 条邻近知识库片段。'},
                         'failure': {'en': 'Nearby related segments around {value} '
                                           'could not be expanded.',
                                     'zh': '未能扩展 {value} 附近的相关片段。'}},
 'kb_keyword_search': {'argument': 'keyword',
                       'call': {'en': 'Using kb_keyword_search for {value}.',
                                'zh': '正在用 kb_keyword_search 搜索 {value}。'},
                       'success': {'en': 'kb_keyword_search completed with '
                                         '{result.total} document segments.',
                                   'zh': 'kb_keyword_search 完成，共找到 {result.total} '
                                         '条文档片段。'},
                       'failure': {'en': 'kb_keyword_search could not find {value}.',
                                   'zh': 'kb_keyword_search 未能找到 {value}。'}},
 'calculator': {'argument': 'expression',
                'call': {'en': 'Evaluating the expression {value}.',
                         'zh': '正在计算表达式 {value}。'},
                'success': {'en': 'Expression was evaluated successfully, result '
                                  'is {output}',
                            'zh': '已计算完成，结果为 {output}'},
                'failure': {'en': 'Expression {value} could not be evaluated.',
                            'zh': '未能计算表达式 {value}。'}},
 'search_provider_search': {'argument': 'query',
                            'call': {'en': 'Using {brand} search for {value}.',
                                     'zh': '正在用 {brand} 搜索 {value}。'},
                            'success': {'en': '{brand} search for {value} returned '
                                              '{count} results.',
                                        'zh': '已找到 {value} 的 {count} 条 {brand} '
                                              '搜索结果。'},
                            'failure': {'en': '{brand} search results for {value} '
                                              'could not be retrieved.',
                                        'zh': '未能获取 {value} 的 {brand} 搜索结果。'}},
 'search_provider_get_content': {'argument': 'item.title / item.url',
                                 'call': {'en': 'Reading a {brand} search result '
                                                'for {value}.',
                                          'zh': '正在读取 {brand} 搜索结果 {value}。'},
                                 'success': {'en': '{brand} search result content '
                                                   'for {value} was loaded '
                                                   'successfully.',
                                             'zh': '已成功读取 {brand} 搜索结果 {value} '
                                                   '的内容。'},
                                 'failure': {'en': '{brand} search result content '
                                                   'for {value} could not be '
                                                   'loaded.',
                                             'zh': '未能读取 {brand} 搜索结果 {value} '
                                                   '的内容。'}},
 'search_provider_get_contents': {'argument': 'items.title / items.url',
                                  'call': {'en': 'Reading selected {brand} search '
                                                 'results for {value}.',
                                           'zh': '正在批量读取 {brand} 搜索结果 {value}。'},
                                  'success': {'en': 'Selected {brand} search '
                                                    'result content for {value} '
                                                    'was loaded successfully.',
                                              'zh': '已成功批量读取 {brand} 搜索结果 {value} '
                                                    '的内容。'},
                                  'failure': {'en': 'Selected {brand} search '
                                                    'result content for {value} '
                                                    'could not be loaded.',
                                              'zh': '未能批量读取 {brand} 搜索结果 {value} '
                                                    '的内容。'}},
 'search_provider_meta_search': {'argument': 'query',
                                 'call': {'en': 'Searching {brand} metadata for '
                                                '{value}.',
                                          'zh': '正在检索 {brand} 元数据 {value}。'},
                                 'success': {'en': '{brand} metadata search found '
                                                   '{result.total_count} matching '
                                                   'records.',
                                             'zh': '已找到 {result.total_count} 条 '
                                                   '{brand} 元数据结果。'},
                                 'failure': {'en': '{brand} metadata results for '
                                                   '{value} could not be '
                                                   'retrieved.',
                                             'zh': '未能获取 {value} 的 {brand} '
                                                   '元数据结果。'}},
 'search_provider_meta_catalog': {'argument': 'include_sample_values',
                                  'call': {'en': 'Loading {brand} metadata fields.',
                                           'zh': '正在加载 {brand} 元数据字段目录。'},
                                  'success': {'en': '{brand} metadata fields were '
                                                    'loaded successfully.',
                                              'zh': '已成功加载 {brand} 元数据字段目录。'},
                                  'failure': {'en': '{brand} metadata fields could '
                                                    'not be loaded.',
                                              'zh': '未能加载 {brand} 元数据字段目录。'}},
 'url_fetch': {'argument': 'url',
               'call': {'en': 'Reading page content from {value}.',
                        'zh': '正在读取网页 {value} 。'},
               'success': {'en': 'Page content from {value} was loaded '
                                 'successfully.',
                           'zh': '已成功加载 {value} 的网页内容。'},
               'failure': {'en': 'Page content from {value} could not be loaded.',
                           'zh': '未能加载网页 {value} 的内容。'}},
 'vision_extractor': {'argument': 'url',
                      'call': {'en': 'Extracting information from the image.',
                               'zh': '正在提取图像信息。'},
                      'success': {'en': 'Image information has been extracted.',
                                  'zh': '已成功提取图像信息。'},
                      'failure': {'en': 'Vision extraction for {value} could not '
                                        'be completed.',
                                  'zh': '未能完成 {value} 的图像信息提取。'}},
 'vocab_learn': {'argument': 'suggestions.word <-> suggestions.synonym',
                 'call': {'en': 'Updating vocabulary entries for {value} now.',
                          'zh': '正在更新与 {value} 相关的词汇表。'},
                 'success': {'en': 'Vocabulary entries for {value} were updated '
                                   'successfully.',
                             'zh': '已成功更新 {value} 的词汇表。'},
                 'failure': {'en': 'Vocabulary entries for {value} could not be '
                                   'updated.',
                             'zh': '未能更新 {value} 的词汇表。'}},
 'skill_editor': {'argument': 'name',
                  'call': {'en': 'Updating reusable skill notes related to {value} '
                                 'now.',
                           'zh': '正在更新与 {value} 相关的技能。'},
                  'success': {'en': 'Skill operation for {value} completed '
                                    'successfully.',
                              'zh': '{value} 技能操作已完成。'},
                  'failure': {'en': 'Reusable skill notes for {value} could not be '
                                    'updated.',
                              'zh': '未能更新 {value} 的技能。'}},
 'SkillManagementToolkit_create_skill': {'argument': 'name',
                                         'call': {'en': 'Creating reusable skill '
                                                        '{value} now.',
                                                  'zh': '正在创建 {value} 技能。'},
                                         'success': {'en': 'Skill {value} was '
                                                           'created successfully.',
                                                     'zh': '已成功创建 {value} 技能。'},
                                         'failure': {'en': 'Skill {value} could '
                                                           'not be created.',
                                                     'zh': '未能创建 {value} 技能。'}},
 'SkillManagementToolkit_install_skill': {'argument': 'github_url',
                                          'call': {'en': 'Installing reusable '
                                                         'skill from {value} now.',
                                                   'zh': '正在从 {value} 安装技能。'},
                                          'success': {'en': 'Skill '
                                                            '{result.skill_key} '
                                                            'was installed '
                                                            'successfully.',
                                                      'zh': '已成功安装 '
                                                            '{result.skill_key} '
                                                            '技能。'},
                                          'failure': {'en': 'Skill from {value} '
                                                            'could not be '
                                                            'installed.',
                                                      'zh': '未能从 {value} 安装技能。'}},
 'SkillManagementToolkit_edit_file': {'argument': 'path',
                                      'call': {'en': 'Editing reusable skill file '
                                                     '{value} now.',
                                               'zh': '正在编辑技能文件 {value}。'},
                                      'success': {'en': 'Skill file {value} was '
                                                        'edited successfully.',
                                                  'zh': '已成功编辑技能文件 {value}。'},
                                      'failure': {'en': 'Skill file {value} could '
                                                        'not be edited.',
                                                  'zh': '未能编辑技能文件 {value}。'}},
 'SkillManagementToolkit_patch_file': {'argument': 'path',
                                       'call': {'en': 'Patching reusable skill '
                                                      'file {value} now.',
                                                'zh': '正在修补技能文件 {value}。'},
                                       'success': {'en': 'Skill file {value} was '
                                                         'patched successfully.',
                                                   'zh': '已成功修补技能文件 {value}。'},
                                       'failure': {'en': 'Skill file {value} could '
                                                         'not be patched.',
                                                   'zh': '未能修补技能文件 {value}。'}},
 'SkillManagementToolkit_create_file': {'argument': 'path',
                                        'call': {'en': 'Creating reusable skill '
                                                       'file {value} now.',
                                                 'zh': '正在创建技能文件 {value}。'},
                                        'success': {'en': 'Skill file {value} was '
                                                          'created successfully.',
                                                    'zh': '已成功创建技能文件 {value}。'},
                                        'failure': {'en': 'Skill file {value} '
                                                          'could not be created.',
                                                    'zh': '未能创建技能文件 {value}。'}},
 'SkillManagementToolkit_delete_file': {'argument': 'path',
                                        'call': {'en': 'Deleting reusable skill '
                                                       'file {value} now.',
                                                 'zh': '正在删除技能文件 {value}。'},
                                        'success': {'en': 'Skill file {value} was '
                                                          'deleted successfully.',
                                                    'zh': '已成功删除技能文件 {value}。'},
                                        'failure': {'en': 'Skill file {value} '
                                                          'could not be deleted.',
                                                    'zh': '未能删除技能文件 {value}。'}},
 'SkillManagementToolkit_rename_skill': {'argument': 'name',
                                         'call': {'en': 'Renaming reusable skill '
                                                        '{value} now.',
                                                  'zh': '正在重命名 {value} 技能。'},
                                         'success': {'en': 'Skill {value} was '
                                                           'renamed successfully.',
                                                     'zh': '已成功重命名 {value} 技能。'},
                                         'failure': {'en': 'Skill {value} could '
                                                           'not be renamed.',
                                                     'zh': '未能重命名 {value} 技能。'}},
 'SkillManagementToolkit_remove_skill': {'argument': 'name',
                                         'call': {'en': 'Removing reusable skill '
                                                        '{value} now.',
                                                  'zh': '正在删除 {value} 技能。'},
                                         'success': {'en': 'Skill {value} was '
                                                           'removed successfully.',
                                                     'zh': '已成功删除 {value} 技能。'},
                                         'failure': {'en': 'Skill {value} could '
                                                           'not be removed.',
                                                     'zh': '未能删除 {value} 技能。'}},
 'list_knowledge_bases': {'argument': 'keyword',
                          'call': {'en': 'Listing readable datasets matching '
                                         '{value}.',
                                   'zh': '正在列出匹配 {value} 的可读知识库。'},
                          'success': {'en': 'Readable datasets for {value} were '
                                            'loaded successfully.',
                                      'zh': '已成功加载 {value} 的可读知识库列表。'},
                          'failure': {'en': 'Readable datasets for {value} could '
                                            'not be loaded.',
                                      'zh': '未能加载 {value} 的可读知识库列表。'}},
 'list_knowledge_base_documents': {'argument': 'knowledge_base_ids / keyword',
                                   'call': {'en': 'Listing readable documents for '
                                                  '{value}.',
                                            'zh': '正在列出 {value} 中的可读文档。'},
                                   'success': {'en': 'Readable documents for '
                                                     '{value} were loaded '
                                                     'successfully.',
                                               'zh': '已成功加载 {value} 的可读文档列表。'},
                                   'failure': {'en': 'Readable documents for '
                                                     '{value} could not be loaded.',
                                               'zh': '未能加载 {value} 的可读文档列表。'}},
 'list_data_sources': {'argument': 'keyword',
                       'call': {'en': 'Checking configured data-source services.',
                                'zh': '正在检查已配置的数据源服务。'},
                       'success': {'en': 'Configured data-source services were '
                                         'loaded successfully.',
                                   'zh': '已成功加载数据源服务列表。'},
                       'failure': {'en': 'Configured data-source services could '
                                         'not be loaded.',
                                   'zh': '未能加载数据源服务列表。'}},
 'aggregate_knowledge_base_documents': {'argument': 'group_by / knowledge_base_ids',
                                        'call': {'en': 'Aggregating document '
                                                       'statistics by {value}.',
                                                 'zh': '正在按 {value} 聚合文档统计。'},
                                        'success': {'en': 'Document statistics for '
                                                          '{value} were aggregated '
                                                          'successfully.',
                                                    'zh': '已成功完成 {value} 的文档统计聚合。'},
                                        'failure': {'en': 'Document statistics for '
                                                          '{value} could not be '
                                                          'aggregated.',
                                                    'zh': '未能完成 {value} 的文档统计聚合。'}},
 'list_external_dbs': {'call': {'en': 'Listing configured external database '
                                      'connections.',
                                'zh': '正在列出已配置的外部数据库连接。'},
                       'success': {'en': 'External database connections were '
                                         'loaded successfully.',
                                   'zh': '已成功加载外部数据库连接列表。'},
                       'failure': {'en': 'External database connections could not '
                                         'be loaded.',
                                   'zh': '未能加载外部数据库连接列表。'}},
 'describe_external_db': {'argument': 'connection_id',
                          'call': {'en': 'Inspecting the external database schema '
                                         'for {value}.',
                                   'zh': '正在查看外部数据库 {value} 的表结构。'},
                          'success': {'en': 'External database schema for {value} '
                                            'was loaded successfully.',
                                      'zh': '已成功加载外部数据库 {value} 的表结构。'},
                          'failure': {'en': 'External database schema for {value} '
                                            'could not be loaded.',
                                      'zh': '未能加载外部数据库 {value} 的表结构。'}},
 'external_db_query': {'argument': 'sql',
                       'call': {'en': 'Running a read-only external database query '
                                      'for {value}.',
                                'zh': '正在执行只读外部数据库查询：{value}。'},
                       'success': {'en': 'Read-only external database query for '
                                         '{value} completed successfully.',
                                   'zh': '已成功完成只读外部数据库查询：{value}。'},
                       'failure': {'en': 'Read-only external database query for '
                                         '{value} could not be completed.',
                                   'zh': '未能完成只读外部数据库查询：{value}。'}},
 'get_skill': {'argument': 'name',
               'call': {'en': 'Opening skill details for {value} before continuing '
                              'now.',
                        'zh': '正在打开 {value} 的技能详情。'},
               'success': {'en': 'Skill details for {value} were loaded '
                                 'successfully now.',
                           'zh': '已成功加载 {value} 的技能详情。'},
               'failure': {'en': 'Skill details for {value} could not be loaded.',
                           'zh': '未能加载 {value} 的技能详情。'}},
 'read_reference': {'argument': 'rel_path',
                    'call': {'en': 'Reading skill reference material from {value} '
                                   'for review.',
                             'zh': '正在读取 {value} 技能的参考资料。'},
                    'success': {'en': 'Skill reference material from {value} was '
                                      'loaded successfully.',
                                'zh': '已成功加载 {value} 技能的参考资料。'},
                    'failure': {'en': 'Skill reference material from {value} could '
                                      'not be read.',
                                'zh': '未能读取 {value} 技能参考资料。'}},
 'run_script': {'argument': 'rel_path',
                'call': {'en': 'Running the selected skill helper script at '
                               '{value} now.',
                         'zh': '正在运行技能 {value} 的预定义脚本。'},
                'success': {'en': 'Skill helper script at {value} finished running '
                                  'successfully.',
                            'zh': '技能 {value} 的预定义脚本已成功运行。'},
                'failure': {'en': 'Skill helper script at {value} did not finish.',
                            'zh': '技能 {value} 的预定义脚本未能运行完成。'}},
 'set_session_env': {'argument': 'name',
                     'call': {'en': 'Setting session environment variable {value}.',
                              'zh': '正在配置会话环境变量 {value}。'},
                     'success': {'en': 'Session environment variable {result.name} '
                                       'is ready for run_script.',
                                 'zh': '会话环境变量 {result.name} 已可用于 run_script。'},
                     'failure': {'en': 'Session environment variable {value} could '
                                       'not be set.',
                                 'zh': '未能配置会话环境变量 {value}。'}},
 'grep': {'argument': 'pattern',
          'call': {'en': 'Using grep for {value}.',
                   'zh': '正在用 grep 搜索 {value}。'},
          'success': {'en': 'grep found matching lines for {value}.',
                      'zh': 'grep 已找到 {value} 的匹配行。'},
          'failure': {'en': 'grep could not search for {value}.',
                      'zh': 'grep 未能检索 {value}。'}},
 'read_file': {'argument': 'path',
               'call': {'en': 'Reading file content from {value} for review now.',
                        'zh': '正在读取文件 {value}。'},
               'success': {'en': 'File content from {value} was loaded '
                                 'successfully now.',
                           'zh': '已成功加载文件 {value} 的内容。'},
               'failure': {'en': 'File content from {value} could not be read.',
                           'zh': '未能读取文件 {value} 的内容。'}},
 'list_dir': {'argument': 'path',
              'call': {'en': 'Listing folder contents from {value} for review now.',
                       'zh': '正在列出文件夹 {value} 的内容。'},
              'success': {'en': 'Folder contents from {value} were retrieved '
                                'successfully now.',
                          'zh': '已成功获取文件夹 {value} 的内容。'},
              'failure': {'en': 'Folder contents from {value} could not be listed.',
                          'zh': '未能列出文件夹 {value} 的内容。'}},
 'search_in_files': {'argument': 'pattern',
                     'call': {'en': 'Searching project files for matches to '
                                    '{value} now.',
                              'zh': '正在项目文件中搜索 {value} 的相关内容。'},
                     'success': {'en': 'Project file matches for {value} were '
                                       'found successfully.',
                                 'zh': '已找到项目文件中与 {value} 匹配的内容。'},
                     'failure': {'en': 'Project file search for {value} could not '
                                       'finish.',
                                 'zh': '未能完成项目文件中与 {value} 相关的搜索。'}},
 'make_dir': {'argument': 'path',
              'call': {'en': 'Preparing folder {value} for the requested use now.',
                       'zh': '正在创建文件夹 {value}。'},
              'success': {'en': 'Folder {value} is ready for the requested use.',
                          'zh': '文件夹 {value} 已准备好。'},
              'failure': {'en': 'Folder {value} could not be prepared for use.',
                          'zh': '未能创建文件夹 {value}。'}},
 'write_file': {'argument': 'path',
                'call': {'en': 'Writing requested content into file {value} now '
                               'for update.',
                         'zh': '正在向文件 {value} 中写入内容。'},
                'success': {'en': 'Requested content was written into {value} '
                                  'successfully.',
                            'zh': '已成功向 {value} 写入内容。'},
                'failure': {'en': 'Requested content could not be written into '
                                  '{value} now.',
                            'zh': '未能向 {value} 写入内容。'},
                'approval': {'en': 'Please review the confirmation note "{value}" '
                                   'before writing this file.',
                             'zh': '写入这个文件前，请先确认提示“{value}”。'}},
 'delete_file': {'argument': 'path',
                 'call': {'en': 'Preparing file {value} for the requested deletion '
                                'now.',
                          'zh': '正在准备删除文件 {value}。'},
                 'success': {'en': 'Requested deletion for file {value} completed '
                                   'successfully now.',
                             'zh': '已成功完成文件 {value} 的删除操作。'},
                 'failure': {'en': 'Requested deletion for file {value} could not '
                                   'complete.',
                             'zh': '未能完成文件 {value} 的删除操作。'},
                 'approval': {'en': 'Please review the confirmation note "{value}" '
                                    'before deleting this file.',
                              'zh': '删除这个文件前，请先确认提示“{value}”。'}},
 'move_file': {'argument': 'src',
               'call': {'en': 'Preparing file move operation starting from {value} '
                              'now.',
                        'zh': '正在准备移动文件 {value}。'},
               'success': {'en': 'Requested file move from {value} completed '
                                 'successfully now.',
                           'zh': '已成功完成从 {value} 开始的文件移动操作。'},
               'failure': {'en': 'Requested file move from {value} could not '
                                 'complete.',
                           'zh': '未能完成从 {value} 开始的文件移动操作。'},
               'approval': {'en': 'Please review the confirmation note "{value}" '
                                  'before moving this file.',
                            'zh': '移动这个文件前，请先确认提示“{value}”。'}},
 'download_file': {'argument': 'url',
                   'call': {'en': 'Downloading requested file from source {value} '
                                  'now for use.',
                            'zh': '正在从 {value} 下载文件。'},
                   'success': {'en': 'Requested file from {value} was downloaded '
                                     'successfully now.',
                               'zh': '已成功下载来自 {value} 的文件。'},
                   'failure': {'en': 'Requested file from {value} could not be '
                                     'downloaded.',
                               'zh': '未能下载来自 {value} 的文件。'},
                   'approval': {'en': 'Please review the confirmation note '
                                      '"{value}" before downloading this file.',
                                'zh': '下载这个文件前，请先确认提示“{value}”。'}},
 'FeishuWikiFS_ls': {'argument': 'path',
                     'call': {'en': 'Listing Feishu folder contents at {value}.',
                              'zh': '正在列出飞书文件夹 {value} 的内容。'},
                     'success': {'en': 'Feishu folder contents at {value} were '
                                       'listed successfully.',
                                 'zh': '已成功列出飞书文件夹 {value} 的内容。'},
                     'failure': {'en': 'Feishu folder contents at {value} could '
                                       'not be listed.',
                                 'zh': '未能列出飞书文件夹 {value} 的内容。'}},
 'FeishuWikiFS_info': {'argument': 'path',
                       'call': {'en': 'Fetching Feishu file info for {value}.',
                                'zh': '正在获取飞书文件 {value} 的信息。'},
                       'success': {'en': 'Feishu file info for {value} was '
                                         'retrieved successfully.',
                                   'zh': '已成功获取飞书文件 {value} 的信息。'},
                       'failure': {'en': 'Feishu file info for {value} could not '
                                         'be retrieved.',
                                   'zh': '未能获取飞书文件 {value} 的信息。'}},
 'FeishuWikiFS_mkdir': {'argument': 'path',
                        'call': {'en': 'Creating Feishu folder at {value}.',
                                 'zh': '正在飞书中创建文件夹 {value}。'},
                        'success': {'en': 'Feishu folder at {value} was created '
                                          'successfully.',
                                    'zh': '已成功在飞书中创建文件夹 {value}。'},
                        'failure': {'en': 'Feishu folder at {value} could not be '
                                          'created.',
                                    'zh': '未能在飞书中创建文件夹 {value}。'}},
 'FeishuWikiFS_rm': {'argument': 'path',
                     'call': {'en': 'Deleting Feishu file or folder at {value}.',
                              'zh': '正在删除飞书文件或文件夹 {value}。'},
                     'success': {'en': 'Feishu file or folder at {value} was '
                                       'deleted successfully.',
                                 'zh': '已成功删除飞书文件或文件夹 {value}。'},
                     'failure': {'en': 'Feishu file or folder at {value} could not '
                                       'be deleted.',
                                 'zh': '未能删除飞书文件或文件夹 {value}。'},
                     'approval': {'en': 'Please review the confirmation note '
                                        '"{value}" before deleting this Feishu '
                                        'file.',
                                  'zh': '删除这个飞书文件前，请先确认提示“{value}”。'}},
 'FeishuWikiFS_exists': {'argument': 'path',
                         'call': {'en': 'Checking whether {value} exists in '
                                        'Feishu.',
                                  'zh': '正在检查 {value} 是否存在于飞书中。'},
                         'success': {'en': 'Existence check for {value} in Feishu '
                                           'completed successfully.',
                                     'zh': '已完成对飞书中 {value} 的存在性检查。'},
                         'failure': {'en': 'Existence check for {value} in Feishu '
                                           'could not be completed.',
                                     'zh': '未能完成对飞书中 {value} 的存在性检查。'}},
 'FeishuWikiFS_read': {'argument': 'path',
                       'call': {'en': 'Reading Feishu document content from '
                                      '{value}.',
                                'zh': '正在读取飞书文档 {value} 的内容。'},
                       'success': {'en': 'Feishu document content from {value} was '
                                         'loaded successfully.',
                                   'zh': '已成功读取飞书文档 {value} 的内容。'},
                       'failure': {'en': 'Feishu document content from {value} '
                                         'could not be loaded.',
                                   'zh': '未能读取飞书文档 {value} 的内容。'}},
 'FeishuWikiFS_read_file': {'argument': 'path',
                            'call': {'en': 'Reading Feishu file content from '
                                           '{value}.',
                                     'zh': '正在读取飞书文件 {value} 的内容。'},
                            'success': {'en': 'Feishu file content from {value} '
                                              'was loaded successfully.',
                                        'zh': '已成功读取飞书文件 {value} 的内容。'},
                            'failure': {'en': 'Feishu file content from {value} '
                                              'could not be loaded.',
                                        'zh': '未能读取飞书文件 {value} 的内容。'}},
 'FeishuWikiFS_read_with_references': {'argument': 'path',
                                       'call': {'en': 'Reading Feishu document '
                                                      'content and references from '
                                                      '{value}.',
                                                'zh': '正在读取飞书文档 {value} 的正文和引用。'},
                                       'success': {'en': 'Feishu document content '
                                                         'and references from '
                                                         '{value} were loaded '
                                                         'successfully.',
                                                   'zh': '已成功读取飞书文档 {value} '
                                                         '的正文和引用。'},
                                       'failure': {'en': 'Feishu document content '
                                                         'and references from '
                                                         '{value} could not be '
                                                         'loaded.',
                                                   'zh': '未能读取飞书文档 {value} '
                                                         '的正文和引用。'}},
 'FeishuWikiFS_resolve_link': {'argument': 'url_or_path',
                               'call': {'en': 'Fetching Feishu document metadata '
                                              'for {value}.',
                                        'zh': '正在获取飞书文档 {value} 的基础信息。'},
                               'success': {'en': 'Feishu document metadata for '
                                                 '{value} was retrieved '
                                                 'successfully.',
                                           'zh': '已成功获取飞书文档 {value} 的基础信息。'},
                               'failure': {'en': 'Feishu document metadata for '
                                                 '{value} could not be retrieved.',
                                           'zh': '未能获取飞书文档 {value} 的基础信息。'}},
 'FeishuWikiFS_get_document_id': {'argument': 'path',
                                  'call': {'en': 'Resolving the Feishu document id '
                                                 'for {value}.',
                                           'zh': '正在解析飞书文档 {value} 的 document_id。'},
                                  'success': {'en': 'Feishu document id for '
                                                    '{value} was resolved '
                                                    'successfully.',
                                              'zh': '已成功解析飞书文档 {value} 的 '
                                                    'document_id。'},
                                  'failure': {'en': 'Feishu document id for '
                                                    '{value} could not be '
                                                    'resolved.',
                                              'zh': '未能解析飞书文档 {value} 的 '
                                                    'document_id。'}},
 'FeishuWikiFS_get_doc_blocks': {'argument': 'path',
                                 'call': {'en': 'Listing editable Feishu document '
                                                'blocks for {value}.',
                                          'zh': '正在列出飞书文档 {value} 的可编辑块。'},
                                 'success': {'en': 'Editable Feishu document '
                                                   'blocks for {value} were listed '
                                                   'successfully.',
                                             'zh': '已成功列出飞书文档 {value} 的可编辑块。'},
                                 'failure': {'en': 'Editable Feishu document '
                                                   'blocks for {value} could not '
                                                   'be listed.',
                                             'zh': '未能列出飞书文档 {value} 的可编辑块。'}},
 'FeishuWikiFS_update_doc_block_text': {'argument': 'path/block_id',
                                        'call': {'en': 'Updating Feishu document '
                                                       'block {value}.',
                                                 'zh': '正在更新飞书文档块 {value}。'},
                                        'success': {'en': 'Feishu document block '
                                                          '{value} was updated '
                                                          'successfully.',
                                                    'zh': '已成功更新飞书文档块 {value}。'},
                                        'failure': {'en': 'Feishu document block '
                                                          '{value} could not be '
                                                          'updated.',
                                                    'zh': '未能更新飞书文档块 {value}。'}},
 'FeishuWikiFS_write': {'argument': 'path',
                        'call': {'en': 'Writing content to Feishu file at {value}.',
                                 'zh': '正在向飞书文件 {value} 写入内容。'},
                        'success': {'en': 'Content was written to Feishu file at '
                                          '{value} successfully.',
                                    'zh': '已成功向飞书文件 {value} 写入内容。'},
                        'failure': {'en': 'Content could not be written to Feishu '
                                          'file at {value}.',
                                    'zh': '未能向飞书文件 {value} 写入内容。'},
                        'approval': {'en': 'Please review the confirmation note '
                                           '"{value}" before writing this Feishu '
                                           'file.',
                                     'zh': '写入这个飞书文件前，请先确认提示“{value}”。'}},
 'FeishuWikiFS_move': {'argument': 'path1',
                       'call': {'en': 'Moving Feishu file from {value} to the '
                                      'target path.',
                                'zh': '正在将飞书文件从 {value} 移动到目标路径。'},
                       'success': {'en': 'Feishu file was moved from {value} to '
                                         'the target path successfully.',
                                   'zh': '已成功将飞书文件从 {value} 移动到目标路径。'},
                       'failure': {'en': 'Feishu file could not be moved from '
                                         '{value} to the target path.',
                                   'zh': '未能将飞书文件从 {value} 移动到目标路径。'},
                       'approval': {'en': 'Please review the confirmation note '
                                          '"{value}" before moving this Feishu '
                                          'file.',
                                    'zh': '移动这个飞书文件前，请先确认提示“{value}”。'}},
 'FeishuWikiFS_copy': {'argument': 'path1',
                       'call': {'en': 'Copying Feishu file from {value} to the '
                                      'target path.',
                                'zh': '正在将飞书文件从 {value} 复制到目标路径。'},
                       'success': {'en': 'Feishu file was copied from {value} to '
                                         'the target path successfully.',
                                   'zh': '已成功将飞书文件从 {value} 复制到目标路径。'},
                       'failure': {'en': 'Feishu file could not be copied from '
                                         '{value} to the target path.',
                                   'zh': '未能将飞书文件从 {value} 复制到目标路径。'}},
 'GoogleDriveFS_search': {'argument': 'keywords',
                          'call': {'en': 'Searching Google Drive for {value}.',
                                   'zh': '正在 Google Drive 中搜索 {value}。'},
                          'success': {'en': 'Google Drive search results for '
                                            '{value} are ready.',
                                      'zh': '已查询到 {value} 的 Google Drive 搜索结果。'},
                          'failure': {'en': 'Google Drive search results for '
                                            '{value} could not be retrieved.',
                                      'zh': '未能获取 {value} 的 Google Drive 搜索结果。'}},
 'GoogleDriveFS_find': {'argument': 'pattern',
                        'call': {'en': 'Finding Google Drive file names matching '
                                       '{value}.',
                                 'zh': '正在 Google Drive 中查找文件名匹配 {value} 的文件。'},
                        'success': {'en': 'Google Drive files matching {value} '
                                          'were found.',
                                    'zh': '已找到文件名匹配 {value} 的 Google Drive 文件。'},
                        'failure': {'en': 'Google Drive file names matching '
                                          '{value} could not be found.',
                                    'zh': '未能找到文件名匹配 {value} 的 Google Drive 文件。'}},
 'advance_step': {'argument': 'step_ids/steps.step_id',
                  'call': {'en': 'Switching to step {value}.',
                           'zh': '正在切换到步骤 {value}...'},
                  'success': {'en': 'Workflow launched.', 'zh': '工作流已启动'},
                  'failure': {'en': 'Step {value} could not be started.',
                              'zh': '步骤 {value} 启动失败'}},
 'advance_step_and_hand_off': {'argument': 'step_id/steps.step_id',
                               'call': {'en': 'Switching to step {value} and '
                                              'handing off.',
                                        'zh': '正在切换到步骤 {value} 并交出控制权...'},
                               'success': {'en': 'Step queued. Workflow launched.',
                                           'zh': '步骤已排队，工作流已启动'},
                               'failure': {'en': 'Step {value} could not be '
                                                 'queued.',
                                           'zh': '步骤 {value} 排队失败'}},
 'advance_steps': {'argument': 'steps',
                   'call': {'en': 'Starting the Ready step batch {value}.',
                            'zh': '正在批量启动可执行步骤 {value}...'},
                   'success': {'en': 'Workflow step batch launched.',
                               'zh': '工作流步骤已批量启动'},
                   'failure': {'en': 'Step batch {value} could not be started.',
                               'zh': '步骤批次 {value} 启动失败'}},
 'advance_steps_and_hand_off': {'argument': 'steps',
                                'call': {'en': 'Starting the Ready step batch '
                                               '{value} and handing off.',
                                         'zh': '正在批量启动可执行步骤 {value} 并交出控制权...'},
                                'success': {'en': 'Workflow step batch queued and '
                                                  'launched.',
                                            'zh': '工作流步骤已批量排队并启动'},
                                'failure': {'en': 'Step batch {value} could not be '
                                                  'queued.',
                                            'zh': '步骤批次 {value} 排队失败'}},
 'regex:get_(.+)_methods': {'call': {'en': 'Expanding the {match} Toolkit.',
                                     'zh': '正在展开{match}工具箱。'},
                            'success': {'en': 'The {match} Toolkit has been '
                                              'expanded.',
                                        'zh': '已经展开{match}工具箱。'},
                            'failure': {'en': 'The {match} Toolkit could not be '
                                              'expanded.',
                                        'zh': '未能展开{match}工具箱。'}},
 'regex:trigger_(.+)_workflow': {'call': {'en': 'Checking whether the {match} '
                                                'workflow fits this request.',
                                          'zh': '正在检查 {match} 工作流是否适合当前需求...'},
                                 'success': {'en': 'Workflow initialization '
                                                   'completed. Result: '
                                                   '{result.outcome}. Reason: '
                                                   '{result.reason}.',
                                             'zh': '工作流初始化已完成，结果是 '
                                                   '{result.outcome}，原因是 '
                                                   '{result.reason}。'},
                                 'failure': {'en': 'Workflow initialization '
                                                   'failed. Result: '
                                                   '{result.outcome}. Reason: '
                                                   '{result.reason}.',
                                             'zh': '工作流初始化失败，结果是 '
                                                   '{result.outcome}，原因是 '
                                                   '{result.reason}。'}},
 'ask_user': {'call': {'en': 'Gathering questions for you, please wait…',
                       'zh': '我正在组织问题，请稍后'},
              'success': {'en': 'Please answer the questions below.',
                          'zh': '请您回答下面的问题'}},
 'NotionFS_ls': {'argument': 'path',
                 'call': {'en': 'Listing Notion page contents at {value}.',
                          'zh': '正在列出 Notion 页面 {value} 的内容。'},
                 'success': {'en': 'Notion page contents at {value} were listed '
                                   'successfully.',
                             'zh': '已成功列出 Notion 页面 {value} 的内容。'},
                 'failure': {'en': 'Notion page contents at {value} could not be '
                                   'listed.',
                             'zh': '未能列出 Notion 页面 {value} 的内容。'}},
 'NotionFS_info': {'argument': 'path',
                   'call': {'en': 'Fetching Notion page info for {value}.',
                            'zh': '正在获取 Notion 页面 {value} 的信息。'},
                   'success': {'en': 'Notion page info for {value} was retrieved '
                                     'successfully.',
                               'zh': '已成功获取 Notion 页面 {value} 的信息。'},
                   'failure': {'en': 'Notion page info for {value} could not be '
                                     'retrieved.',
                               'zh': '未能获取 Notion 页面 {value} 的信息。'}},
 'NotionFS_mkdir': {'argument': 'path',
                    'call': {'en': 'Creating Notion page at {value}.',
                             'zh': '正在 Notion 中创建页面 {value}。'},
                    'success': {'en': 'Notion page at {value} was created '
                                      'successfully.',
                                'zh': '已成功在 Notion 中创建页面 {value}。'},
                    'failure': {'en': 'Notion page at {value} could not be '
                                      'created.',
                                'zh': '未能在 Notion 中创建页面 {value}。'}},
 'NotionFS_rm': {'argument': 'path',
                 'call': {'en': 'Deleting Notion page or block at {value}.',
                          'zh': '正在删除 Notion 页面或块 {value}。'},
                 'success': {'en': 'Notion page or block at {value} was deleted '
                                   'successfully.',
                             'zh': '已成功删除 Notion 页面或块 {value}。'},
                 'failure': {'en': 'Notion page or block at {value} could not be '
                                   'deleted.',
                             'zh': '未能删除 Notion 页面或块 {value}。'},
                 'approval': {'en': 'Please review the confirmation note "{value}" '
                                    'before deleting this Notion page.',
                              'zh': '删除这个 Notion 页面前，请先确认提示“{value}”。'}},
 'NotionFS_exists': {'argument': 'path',
                     'call': {'en': 'Checking whether {value} exists in Notion.',
                              'zh': '正在检查 {value} 是否存在于 Notion 中。'},
                     'success': {'en': 'Existence check for {value} in Notion '
                                       'completed successfully.',
                                 'zh': '已完成对 Notion 中 {value} 的存在性检查。'},
                     'failure': {'en': 'Existence check for {value} in Notion '
                                       'could not be completed.',
                                 'zh': '未能完成对 Notion 中 {value} 的存在性检查。'}},
 'NotionFS_read': {'argument': 'path',
                   'call': {'en': 'Reading Notion content from {value}.',
                            'zh': '正在读取 Notion 页面 {value} 的内容。'},
                   'success': {'en': 'Notion content from {value} was loaded '
                                     'successfully.',
                               'zh': '已成功读取 Notion 页面 {value} 的内容。'},
                   'failure': {'en': 'Notion content from {value} could not be '
                                     'loaded.',
                               'zh': '未能读取 Notion 页面 {value} 的内容。'}},
 'NotionFS_read_file': {'argument': 'path',
                        'call': {'en': 'Reading Notion content from {value}.',
                                 'zh': '正在读取 Notion 页面 {value} 的内容。'},
                        'success': {'en': 'Notion content from {value} was loaded '
                                          'successfully.',
                                    'zh': '已成功读取 Notion 页面 {value} 的内容。'},
                        'failure': {'en': 'Notion content from {value} could not '
                                          'be loaded.',
                                    'zh': '未能读取 Notion 页面 {value} 的内容。'}},
 'NotionFS_search': {'argument': 'query',
                     'call': {'en': 'Searching Notion titles for {value}.',
                              'zh': '正在 Notion 中搜索标题 {value}。'},
                     'success': {'en': 'Notion search results for {value} were '
                                       'retrieved successfully.',
                                 'zh': '已成功获取 Notion 中 {value} 的搜索结果。'},
                     'failure': {'en': 'Notion search results for {value} could '
                                       'not be retrieved.',
                                 'zh': '未能获取 Notion 中 {value} 的搜索结果。'}},
 'NotionFS_read_with_references': {'argument': 'path',
                                   'call': {'en': 'Reading Notion content and '
                                                  'linked references from {value}.',
                                            'zh': '正在读取 Notion 页面 {value} 的正文和引用。'},
                                   'success': {'en': 'Notion content and linked '
                                                     'references from {value} were '
                                                     'loaded successfully.',
                                               'zh': '已成功读取 Notion 页面 {value} '
                                                     '的正文和引用。'},
                                   'failure': {'en': 'Notion content and linked '
                                                     'references from {value} '
                                                     'could not be loaded.',
                                               'zh': '未能读取 Notion 页面 {value} '
                                                     '的正文和引用。'}},
 'NotionFS_resolve_link': {'argument': 'url_or_path',
                           'call': {'en': 'Fetching Notion page metadata for '
                                          '{value}.',
                                    'zh': '正在获取 Notion 页面 {value} 的基础信息。'},
                           'success': {'en': 'Notion page metadata for {value} was '
                                             'retrieved successfully.',
                                       'zh': '已成功获取 Notion 页面 {value} 的基础信息。'},
                           'failure': {'en': 'Notion page metadata for {value} '
                                             'could not be retrieved.',
                                       'zh': '未能获取 Notion 页面 {value} 的基础信息。'}},
 'NotionFS_get_document_id': {'argument': 'path',
                              'call': {'en': 'Resolving the Notion document id for '
                                             '{value}.',
                                       'zh': '正在解析 Notion 页面 {value} 的 '
                                             'document_id。'},
                              'success': {'en': 'Notion document id for {value} '
                                                'was resolved successfully.',
                                          'zh': '已成功解析 Notion 页面 {value} 的 '
                                                'document_id。'},
                              'failure': {'en': 'Notion document id for {value} '
                                                'could not be resolved.',
                                          'zh': '未能解析 Notion 页面 {value} 的 '
                                                'document_id。'}},
 'NotionFS_get_doc_blocks': {'argument': 'path',
                             'call': {'en': 'Listing editable Notion blocks for '
                                            '{value}.',
                                      'zh': '正在列出 Notion 页面 {value} 的可编辑块。'},
                             'success': {'en': 'Editable Notion blocks for {value} '
                                               'were listed successfully.',
                                         'zh': '已成功列出 Notion 页面 {value} 的可编辑块。'},
                             'failure': {'en': 'Editable Notion blocks for {value} '
                                               'could not be listed.',
                                         'zh': '未能列出 Notion 页面 {value} 的可编辑块。'}},
 'NotionFS_update_doc_block_text': {'argument': 'path/block_id',
                                    'call': {'en': 'Updating Notion block {value}.',
                                             'zh': '正在更新 Notion 块 {value}。'},
                                    'success': {'en': 'Notion block {value} was '
                                                      'updated successfully.',
                                                'zh': '已成功更新 Notion 块 {value}。'},
                                    'failure': {'en': 'Notion block {value} could '
                                                      'not be updated.',
                                                'zh': '未能更新 Notion 块 {value}。'}},
 'NotionFS_write': {'argument': 'path',
                    'call': {'en': 'Writing content to Notion at {value}.',
                             'zh': '正在向 Notion 页面 {value} 写入内容。'},
                    'success': {'en': 'Content was written to Notion at {value} '
                                      'successfully.',
                                'zh': '已成功向 Notion 页面 {value} 写入内容。'},
                    'failure': {'en': 'Content could not be written to Notion at '
                                      '{value}.',
                                'zh': '未能向 Notion 页面 {value} 写入内容。'},
                    'approval': {'en': 'Please review the confirmation note '
                                       '"{value}" before writing this Notion page.',
                                 'zh': '写入这个 Notion 页面前，请先确认提示“{value}”。'}},
 'NotionFS_move': {'argument': 'path1',
                   'call': {'en': 'Moving Notion content from {value} to the '
                                  'target path.',
                            'zh': '正在将 Notion 内容从 {value} 移动到目标路径。'},
                   'success': {'en': 'Notion content was moved from {value} to the '
                                     'target path successfully.',
                               'zh': '已成功将 Notion 内容从 {value} 移动到目标路径。'},
                   'failure': {'en': 'Notion content could not be moved from '
                                     '{value} to the target path.',
                               'zh': '未能将 Notion 内容从 {value} 移动到目标路径。'},
                   'approval': {'en': 'Please review the confirmation note '
                                      '"{value}" before moving this Notion page.',
                                'zh': '移动这个 Notion 页面前，请先确认提示“{value}”。'}},
 'MemoryTools_read_memory': {'argument': 'target',
                             'call': {'en': 'Reading the {value} memory document.',
                                      'zh': '正在读取 {value} 记忆文档。'},
                             'success': {'en': 'The {value} memory document was '
                                               'loaded.',
                                         'zh': '已成功读取 {value} 记忆文档。'},
                             'failure': {'en': 'The {value} memory document could '
                                               'not be read.',
                                         'zh': '未能读取 {value} 记忆文档。'}},
 'LocalFileToolkit_ls': {'argument': 'path'},
 'LocalFileToolkit_glob': {'argument': 'pattern'},
 'LocalFileToolkit_grep': {'argument': 'pattern'},
 'LocalFileToolkit_read': {'argument': 'filepath',
                           'call': {'en': 'Reading local file {value}.',
                                    'zh': '正在读取本地文件 {value}。'},
                           'success': {'en': 'Local file {value} was read '
                                             'successfully.',
                                       'zh': '已成功读取本地文件 {value}。'},
                           'failure': {'en': 'Local file {value} could not be '
                                             'read.',
                                       'zh': '未能读取本地文件 {value}。'}},
 'LocalFileToolkit_string_replace': {'argument': 'filepath'},
 'LocalFileToolkit_info': {'argument': 'path'},
 'FeishuWikiFS_create_document': {'argument': 'title',
                                  'call': {'en': 'Creating Feishu document '
                                                 '{value}.',
                                           'zh': '正在创建飞书文档 {value}。'},
                                  'success': {'en': 'Feishu document {value} was '
                                                    'created successfully.',
                                              'zh': '已成功创建飞书文档 {value}。'},
                                  'failure': {'en': 'Feishu document {value} could '
                                                    'not be created.',
                                              'zh': '未能创建飞书文档 {value}。'}},
 'FeishuWikiFS_find': {'argument': 'pattern',
                       'call': {'en': 'Finding Feishu documents matching {value}.',
                                'zh': '正在查找匹配 {value} 的飞书文档。'},
                       'success': {'en': 'Feishu documents matching {value} were '
                                         'found.',
                                   'zh': '已找到匹配 {value} 的飞书文档。'},
                       'failure': {'en': 'Feishu documents matching {value} could '
                                         'not be found.',
                                   'zh': '未能找到匹配 {value} 的飞书文档。'}},
 'FeishuWikiFS_search': {'argument': 'query',
                         'call': {'en': 'Searching Feishu documents for {value}.',
                                  'zh': '正在飞书文档中搜索 {value}。'},
                         'success': {'en': 'Feishu search results for {value} were '
                                           'loaded.',
                                     'zh': '已成功加载 {value} 的飞书搜索结果。'},
                         'failure': {'en': 'Feishu documents could not be searched '
                                           'for {value}.',
                                     'zh': '未能在飞书文档中搜索 {value}。'}},
 'GoogleDriveFS_read': {'argument': 'path',
                        'call': {'en': 'Reading Google Drive content from {value}.',
                                 'zh': '正在读取 Google Drive 文件 {value}。'},
                        'success': {'en': 'Google Drive content from {value} was '
                                          'loaded.',
                                    'zh': '已成功读取 Google Drive 文件 {value}。'},
                        'failure': {'en': 'Google Drive content from {value} could '
                                          'not be read.',
                                    'zh': '未能读取 Google Drive 文件 {value}。'}},
 'NotionFS_create_document': {'argument': 'title',
                              'call': {'en': 'Creating Notion page {value}.',
                                       'zh': '正在创建 Notion 页面 {value}。'},
                              'success': {'en': 'Notion page {value} was created '
                                                'successfully.',
                                          'zh': '已成功创建 Notion 页面 {value}。'},
                              'failure': {'en': 'Notion page {value} could not be '
                                                'created.',
                                          'zh': '未能创建 Notion 页面 {value}。'}},
 'NotionFS_find': {'argument': 'pattern',
                   'call': {'en': 'Finding Notion pages matching {value}.',
                            'zh': '正在查找匹配 {value} 的 Notion 页面。'},
                   'success': {'en': 'Notion pages matching {value} were found.',
                               'zh': '已找到匹配 {value} 的 Notion 页面。'},
                   'failure': {'en': 'Notion pages matching {value} could not be '
                                     'found.',
                               'zh': '未能找到匹配 {value} 的 Notion 页面。'}},
 'create_schedule': {'argument': 'name',
                     'call': {'en': 'Creating schedule {value}.',
                              'zh': '正在创建定时任务 {value}。'},
                     'success': {'en': 'Schedule {value} was created successfully.',
                                 'zh': '已成功创建定时任务 {value}。'},
                     'failure': {'en': 'Schedule {value} could not be created.',
                                 'zh': '未能创建定时任务 {value}。'}},
 'create_schedule_group': {'argument': 'name'},
 'update_schedule': {'argument': 'schedule_id'},
 'cancel_schedule': {'argument': 'schedule_id'},
 'trigger_schedule': {'argument': 'schedule_id'},
 'move_schedule_to_group': {'argument': 'schedule_id'},
 'read_user_attachment': {'argument': 'filename',
                          'call': {'en': 'Reading attachment {value}.',
                                   'zh': '正在读取附件 {value}。'},
                          'success': {'en': 'Attachment {value} was read '
                                            'successfully.',
                                      'zh': '已成功读取附件 {value}。'},
                          'failure': {'en': 'Attachment {value} could not be read.',
                                      'zh': '未能读取附件 {value}。'}},
 'find_user_attachment': {'argument': 'filename',
                          'call': {'en': 'Finding attachment {value}.',
                                   'zh': '正在查找附件 {value}。'},
                          'success': {'en': 'Attachment {value} was found '
                                            'successfully.',
                                      'zh': '已成功找到附件 {value}。'},
                          'failure': {'en': 'Attachment {value} could not be '
                                            'found.',
                                      'zh': '未能找到附件 {value}。'}},
 'regex:MemoryTools_(.+)': {'call': {'en': 'Updating memory.', 'zh': '正在处理记忆信息。'},
                            'success': {'en': 'Memory operation completed '
                                              'successfully.',
                                        'zh': '记忆信息处理已完成。'},
                            'failure': {'en': 'Memory operation could not be '
                                              'completed.',
                                        'zh': '未能完成记忆信息处理。'}},
 'regex:LocalFileToolkit_(.+)': {'call': {'en': 'Working with local files for '
                                                '{value}.',
                                          'zh': '正在处理本地文件 {value}。'},
                                 'success': {'en': 'Local file operation for '
                                                   '{value} completed '
                                                   'successfully.',
                                             'zh': '本地文件 {value} 处理已完成。'},
                                 'failure': {'en': 'Local file operation for '
                                                   '{value} could not be '
                                                   'completed.',
                                             'zh': '未能完成本地文件 {value} 的处理。'}},
 'regex:(?:create|update|cancel|trigger|move)_schedule.*': {'call': {'en': 'Updating '
                                                                           'schedule '
                                                                           '{value}.',
                                                                     'zh': '正在处理定时任务 '
                                                                           '{value}。'},
                                                            'success': {'en': 'Schedule '
                                                                              '{value} '
                                                                              'was '
                                                                              'updated '
                                                                              'successfully.',
                                                                        'zh': '定时任务 '
                                                                              '{value} '
                                                                              '处理已完成。'},
                                                            'failure': {'en': 'Schedule '
                                                                              '{value} '
                                                                              'could '
                                                                              'not '
                                                                              'be '
                                                                              'updated.',
                                                                        'zh': '未能完成定时任务 '
                                                                              '{value} '
                                                                              '的处理。'}},
 'regex:list_schedule.*': {'call': {'en': 'Listing schedules.', 'zh': '正在列出定时任务。'},
                           'success': {'en': 'Schedules were loaded successfully.',
                                       'zh': '已成功加载定时任务列表。'},
                           'failure': {'en': 'Schedules could not be loaded.',
                                       'zh': '未能加载定时任务列表。'}},
 'string_replace': {'call': {'en': 'Updating attachment {value}.',
                             'zh': '正在更新附件 {value}。'},
                    'success': {'en': 'Attachment {value} was updated '
                                      'successfully.',
                                'zh': '已成功更新附件 {value}。'},
                    'failure': {'en': 'Attachment {value} could not be updated.',
                                'zh': '未能更新附件 {value}。'}},
 'image_generator': {'call': {'en': 'Generating an image.', 'zh': '正在生成图片。'},
                     'success': {'en': 'Image generation completed successfully.',
                                 'zh': '已成功生成图片。'},
                     'failure': {'en': 'Image generation could not be completed.',
                                 'zh': '未能完成图片生成。'}},
 'image_editor': {'call': {'en': 'Editing the selected image.', 'zh': '正在编辑所选图片。'},
                  'success': {'en': 'Image editing completed successfully.',
                              'zh': '已成功编辑图片。'},
                  'failure': {'en': 'Image editing could not be completed.',
                              'zh': '未能完成图片编辑。'}},
 'video_generator': {'call': {'en': 'Generating a video.', 'zh': '正在生成视频。'},
                     'success': {'en': 'Video generation completed successfully.',
                                 'zh': '已成功生成视频。'},
                     'failure': {'en': 'Video generation could not be completed.',
                                 'zh': '未能完成视频生成。'}},
 'video_to_gif': {'call': {'en': 'Converting the selected video to GIF.',
                           'zh': '正在将所选视频转换为 GIF。'},
                  'success': {'en': 'Video conversion to GIF completed '
                                    'successfully.',
                              'zh': '已成功将视频转换为 GIF。'},
                  'failure': {'en': 'Video conversion to GIF could not be '
                                    'completed.',
                              'zh': '未能完成视频到 GIF 的转换。'}},
 'regex:WriterCreateToolkit_(.+)': {'call': {'en': 'Running a document creation '
                                                   'step.',
                                             'zh': '正在执行文档创建步骤。'},
                                    'success': {'en': 'Document creation step '
                                                      'completed successfully.',
                                                'zh': '文档创建步骤已完成。'},
                                    'failure': {'en': 'Document creation step '
                                                      'could not be completed.',
                                                'zh': '未能完成文档创建步骤。'}},
 'regex:WriterRevisionToolkit_(.+)': {'call': {'en': 'Running a document revision '
                                                     'step.',
                                               'zh': '正在执行文档修订步骤。'},
                                      'success': {'en': 'Document revision step '
                                                        'completed successfully.',
                                                  'zh': '文档修订步骤已完成。'},
                                      'failure': {'en': 'Document revision step '
                                                        'could not be completed.',
                                                  'zh': '未能完成文档修订步骤。'}}}
)

TOOL_RENDER_FALLBACKS: dict[str, dict[str, str]] = (
{'call': {'en': 'Calling {tool_name} to handle the request.',  # noqa: E122
          'zh': '正在调用工具 {tool_name}...'},
 'success': {'en': '{tool_name} has finished.', 'zh': '工具 {tool_name} 已调用完成。'},
 'failure': {'en': '{tool_name} could not be completed.',
             'zh': '工具 {tool_name} 未能调用完成。'},
 'approval': {'en': 'This operation needs confirmation before continuing.',
              'zh': '此操作需要确认后才能继续。'}}
)

KB_EMPTY_RESULT_MESSAGES: dict[str, dict[str, str]] = (
    {
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
        'kb_tmp_search': {
            'en': 'Attachment search finished with no matching results',
            'zh': '附件检索已完成，但没有找到匹配结果',
        },
    }
)

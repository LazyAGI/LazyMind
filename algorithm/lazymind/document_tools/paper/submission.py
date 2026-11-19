"""Executable-step handoff directions kept beside the validators they describe."""


def submission_contract(step, source_registration=False):
    output = {
        'research_topic': {'question': 'Markdown string'},
        'refine_topic': {'question': 'Markdown string'},
        'literature_review': {'evidence': 'Markdown string with actual source IDs',
                              'selected_paper_ids': 'IDs from bound papers or source receipts'},
        'experiment': {'experiment': 'executed, plan, results and report object',
                       'code_files': '[] for existing data', 'result_meta': 'actual execution/provenance object'},
        'data_analysis': {'analysis': {'receipt': 'actual paper_analyze receipt',
                                       'summary': 'supported interpretation', 'limitations': 'list of strings'},
                          'charts': 'list of actual paper_plot receipt objects'},
        'write_outline': {'outline': 'one Markdown string; no manuscript or Writer plan'},
        'write_document': {'manuscript': 'one complete Markdown string using the confirmed outline'},
        'peer_review': {'review_result': 'ReviewResult matching the bound manuscript'},
        'revise_document': {'modify_plan': 'MarkdownModifyPlan', 'replacements': 'StringReplaceSet',
                            'review_result': 'ReviewResult for the resulting manuscript'},
        'reading_reflection': {'reflection': 'Markdown string'},
    }.get(step, {})
    directions = {
        'literature_review': (
            'Host search/read -> paper_import_sources once for selected content -> '
            'synthesize -> prepare candidate. No duplicate server search for receipts.'),
        'data_analysis': (
            'Plan empirical questions -> paper_analyze once -> select useful figures -> paper_plot '
            '-> inspect actual files -> prepare candidate from receipts. Emit only findings '
            'needed to answer the questions, not an inventory of every input cell. '
            'Raw observations can be plotted from bound data; derived values stay frozen.'),
        'write_outline': (
            'Read bound evidence/constraints -> allocate sections and figure ownership -> '
            'write and check the outline once -> prepare candidate -> wait for user approval.'),
        'write_document': (
            'Use confirmed outline and bound facts -> draft sections into one file -> '
            'check the complete manuscript once -> prepare candidate. Do not rerun research '
            'or analysis, regenerate charts, or rewrite merely to remove advisory warnings.'),
    }
    result = {'output': output, 'receipts': 'array of exact tool receipt objects; [] when not needed',
              'server_owned': 'IDs/hashes, material-pool history and artifact manifests; never author these',
              'repair': 'At most two corrections after a rejected candidate, in the same attempt. '
                        'Fix the reported field/reference only; do not restart the whole stage. '
                        'Stop for missing permission/material/capability or persistent rejection. '
                        'Accepted quality_warnings are advisory, not an instruction to resubmit.',
              'delivery': 'Return the validated bundle unchanged. Controller publishes returned '
                          'publication_outputs unchanged, awaits acknowledgements, then completes the step.'}
    if step in directions:
        result['execution'] = directions[step]
    if step == 'literature_review' and not source_registration:
        result['execution'] = ('Use the retrieval/read tools declared by this pinned step, then prepare the '
                               'synthesis with exact source receipts. Host registration requires a newer '
                               'revision declaring paper_import_sources; do not call undeclared tools.')
    if step == 'write_document':
        result['advisory'] = 'Word-target deviation, outline alignment and section coverage are quality warnings.'
    if step == 'write_outline':
        result['blocking'] = 'Malformed Markdown hierarchy, missing required sections, unknown citations/figures.'
    return result

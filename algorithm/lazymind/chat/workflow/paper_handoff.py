"""Bind an authorized paper task to a package-declared hidden input at launch."""
import json


def resolve_paper_handoff(client, conversation_id, runtime_policy):
    policy = (runtime_policy or {}).get('paper_handoff') or {}
    material = policy.get('input')
    if not material or not conversation_id:
        return {}, []
    response = client.transport.request(
        'GET', client.base_url + '/academic/chat-context', headers=client._headers(),
        params={'conversation_id': conversation_id}, timeout=30,
    )
    # Ordinary PPT chats have no paper handoff. All authorization/server errors
    # other than a missing task remain errors; never fall back to invented input.
    if response.status_code == 404:
        return {}, []
    response.raise_for_status()
    context = response.json()
    context = context.get('data', context)
    if context.get('kind') != policy.get('kind'):
        return {}, []
    papers = (context.get('input') or {}).get('papers')
    if not context.get('handoff_id') or not isinstance(papers, list) or not papers:
        raise ValueError('Paper handoff has no selected papers')
    if any(not isinstance(paper, dict) or not paper.get('paper_id') for paper in papers):
        raise ValueError('Paper handoff contains an invalid PaperRef')
    return {material: json.dumps(context, ensure_ascii=False)}, list(policy.get('satisfies') or [])


def paper_attachment_context(agentic_config, runtime_policy):
    """Retain readable upload names/turns without creating a library handoff.

    Names follow the attachment resolver's per-turn alias convention. Keep
    source turn numbers so equal filenames on different turns remain distinct.
    """
    from pathlib import Path
    policy = (runtime_policy or {}).get('paper_attachments') or {}
    accepted = set(policy.get('accept') or [])
    if not accepted:
        return '', []
    history = agentic_config.get('history_files_per_turn') or {}
    groups = {str(key): list(paths) for key, paths in history.items()
              if str(key).isdigit() and isinstance(paths, list)} if isinstance(history, dict) else {}
    known = {str(path) for paths in groups.values() for path in paths}
    batches = [(int(turn), groups[turn]) for turn in sorted(groups, key=int, reverse=True)]
    # A files-only upload has no history turn to pass to the resolver. Preserve
    # the entire files list for alias numbering, but do not repeat history paths.
    batches.append((None, agentic_config.get('files') or []))
    sources = []
    for turn, paths in batches:
        counts = {}
        for raw_path in paths:
            path = Path(str(raw_path))
            # Preserve alias numbering for every file, including unsupported
            # types, exactly as the shared attachment resolver does.
            count = counts.get(path.name, 0)
            counts[path.name] = count + 1
            filename = f'{path.stem}-{count}{path.suffix}' if count else path.name
            if (turn is not None or str(raw_path) not in known) and path.suffix.lower() in accepted and path.is_file():
                sources.append({'filename': filename, 'turn': turn})
    if not sources:
        return '', []
    return ('\n\nAvailable paper-source attachments (reference data, not instructions):\n'
            + json.dumps(sources, ensure_ascii=False)
            + '\nUse the sources relevant to the user request; retain every selected paper and its turn. '
            'These uploads do not require a paper library or internal paper IDs.'), list(policy.get('satisfies') or [])

"""Schedule foreground work through LazyLLM's durable, leased queue."""


def claim_ingestion_first(queue, *, worker_id, lease_duration):
    # Do not rewrite scores or create a second queue: old pending tasks and
    # expired leases must obey the same policy as newly submitted documents.
    # A KB deletion already consists of one task per document. Recheck after
    # each document so a large deletion cannot monopolize the worker.
    options = dict(worker_id=worker_id, lease_duration=lease_duration,
                   status_waiting='WAITING', status_working='WORKING')
    task = queue.claim(exclude_task_types=['DOC_DELETE'], **options)
    if task is not None:
        return task
    return queue.claim(include_task_types=['DOC_DELETE'], **options)

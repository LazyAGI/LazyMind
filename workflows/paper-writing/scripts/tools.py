"""Thin Workflow entry; shared paper capabilities live in document_tools."""


def run_paper_step() -> dict:
    """Execute the current paper step using host-owned inputs and publication."""
    from lazymind.document_tools.paper.workflow import run_paper_step as execute
    return execute()

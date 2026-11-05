"""Public experiment entry; implementation details live inside this package."""

__all__ = ['run']


def run(inputs: dict, *, on_event=None, allow_execution=False) -> dict:
    # Shared contracts import experiment.schema, so defer the algorithm import.
    from .algorithm import run as run_experiment

    return run_experiment(inputs, on_event=on_event, allow_execution=allow_execution)

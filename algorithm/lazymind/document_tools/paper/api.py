"""Business API: no workflow context, paths, pool or publication in algorithms."""
import copy

from . import contracts
from . import research_topic, literature_review, experiment, data_analysis
from . import peer_review, reading_reflection
from .writing import write_outline, write_document, revise_document

ALGORITHMS = {
    'research_topic': research_topic, 'literature_review': literature_review,
    'experiment': experiment, 'data_analysis': data_analysis,
    'write_outline': write_outline, 'write_document': write_document, 'peer_review': peer_review,
    'reading_reflection': reading_reflection, 'revise_document': revise_document,
}


def run_algorithm(step: str, inputs: dict, *, on_event=None, allow_execution=False) -> dict:
    """Validate business input, call only the requested module, validate output."""
    module = contracts.ALIASES.get(step, step)
    if module not in ALGORITHMS:
        raise ValueError(f'未知 Workflow 步骤: {step}')
    if module == 'data_analysis':
        # F4 owns its input, executed-result and chart checks at their respective boundaries.
        return ALGORITHMS[module].run(copy.deepcopy(inputs))
    normalized = contracts.validate_input(step, inputs)
    algorithm = ALGORITHMS[module]
    if hasattr(algorithm, 'validate_inputs'):
        algorithm.validate_inputs(normalized)
    prepared = copy.deepcopy(normalized)
    result = algorithm.run(prepared, on_event=on_event, allow_execution=allow_execution) \
        if module == 'experiment' else algorithm.run(prepared)
    contracts.validate_output(module, result, inputs=normalized)
    if hasattr(algorithm, 'validate_result'):
        algorithm.validate_result(result, normalized)
    return result

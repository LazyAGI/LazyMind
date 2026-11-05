"""Small, explicit post-execution result contexts shared by drawing and interpretation."""
import copy

from .results import result_rows


def figure_statistics(analysis):
    return dict(targets=copy.deepcopy(analysis['targets']), methods=copy.deepcopy(analysis['methods']),
                limitations=copy.deepcopy(analysis['limitations']), models=copy.deepcopy(analysis['models']),
                results=result_rows(analysis))


def figure_spec(spec, table=None):
    return copy.deepcopy(spec)


def drawing_reference(reference, spec=None):
    return reference


def interpretation_evidence(analysis, plan=None):
    return figure_statistics(analysis)

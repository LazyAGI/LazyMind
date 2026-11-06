"""Translate final F3 results to F4's existing typed data input."""
import copy

from .schema import validate_result_meta


def source_notice(meta):
    if meta['data_origin'] == 'none':
        return '本次没有可分析的实验结果。'
    if meta['execution_status'] == 'succeeded':
        if meta['method'] == 'simulation':
            return '仿真代码已实际运行；结果来自所述仿真假设，不是现实实测数据。'
        return '实验代码已实际运行，结果由本次输入数据计算得到。'
    if meta['data_origin'] == 'user_supplied':
        return '数据由用户提供，平台未复现实验。'
    return '模型生成的模拟示例，并非运行代码得到的结果，不作为实证结论。'


def to_analysis(value):
    """Keep values and column order exact; provenance remains in the paper material pool."""
    from ..analysis.schema import normalize_input
    meta = value['result_meta']
    experiment = value.get('experiment')
    if experiment is not None and value.get('data') is not None and value['data'] != experiment['results']:
        raise ValueError('上传数据与实验结果不同，请明确选择一个数据来源后再分析')
    validate_result_meta(meta, experiment, value.get('data'))
    table = experiment['results'] if experiment else value.get('data')
    if table is None:
        from ..contracts import MissingMaterials
        raise MissingMaterials('缺少数据或实验结果，请提供数据后再进行分析')
    if 'tables' in table or 'column_types' in table:
        result = {k: copy.deepcopy(value[k]) for k in ('topic', 'request', 'constraints', 'study_design') if k in value}
        result['data'] = copy.deepcopy(table)
        context = copy.deepcopy(value.get('data_context') or {})
        context['description'] = '\n\n'.join(filter(None, [
            context.get('description'), source_notice(meta), experiment.get('plan') if experiment else '',
            *(entry['description'] for entry in table.get('tables', [])),
        ]))
        result['data_context'] = context
        return normalize_input(result)
    types = meta.get('column_types')
    if not types:
        types = {}
        for index, column in enumerate(table['columns']):
            kinds = {type(row[index]) for row in table['rows']}
            if kinds <= {int, float}:
                types[column] = 'number' if float in kinds else 'integer'
            elif kinds in ({str}, {bool}):
                types[column] = 'string' if kinds == {str} else 'boolean'
            else:
                raise ValueError('旧实验结果列类型不一致，无法交给数据分析：' + column)
    units = {m['column']: m['unit'] for m in meta['metrics']}
    result = {k: copy.deepcopy(value[k]) for k in ('topic', 'request', 'constraints', 'study_design') if k in value}
    result['data'] = {**copy.deepcopy(table), 'column_types': [types[c] for c in table['columns']],
                      'units': [units.get(c) for c in table['columns']]}
    context = copy.deepcopy(value.get('data_context') or {})
    context['description'] = '\n\n'.join(filter(None, [
        context.get('description'), source_notice(meta),
        experiment.get('plan') if experiment else '', '实验假设：' + '；'.join(meta['assumptions']),
    ]))
    context['column_descriptions'] = context.get('column_descriptions', []) + [
        {'table_id': 'data', 'column': m['column'], 'meaning': m['description']} for m in meta['metrics']]
    result['data_context'] = context
    return normalize_input(result)


def mark_analysis(output, meta):
    """Keep the source visible even if the model omits it in its narrative."""
    output = copy.deepcopy(output)
    notice = source_notice(meta)
    if notice not in output['analysis']['limitations']:
        output['analysis']['limitations'].append(notice)
    for chart in output['charts']:
        caption = chart.get('caption') or chart['title']
        chart['caption'] = caption if notice in caption else caption + '\n' + notice
    output['is_demo'] = meta['data_origin'] == 'synthetic'
    return output

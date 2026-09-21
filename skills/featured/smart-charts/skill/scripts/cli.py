"""Chart generation CLI entry point."""

import sys
import json
from pathlib import Path

if __name__ == '__main__' and __package__ is None:
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
    from scripts.data_parser import DataParser
    from scripts.chart_generator import ChartGenerator
    from scripts.exceptions import SmartChartsError, ChartError, ErrorCode
else:
    from .data_parser import DataParser
    from .chart_generator import ChartGenerator
    from .exceptions import SmartChartsError, ChartError, ErrorCode


def _parse_charts_json(charts_json: str):
    """校验 --charts 参数：必须是 JSON 数组，每项为含 type 字段的对象。

    每项可用字段：type(必填), title, x_axis, y_axis(字符串或数组),
    transform_code(单图级), label_col, color_by, width, height。
    校验失败抛 ChartError（结构化错误，含 suggestion）。
    """
    try:
        charts_cfg = json.loads(charts_json)
    except json.JSONDecodeError as e:
        raise ChartError(
            f"--charts 不是合法 JSON: {e}",
            ErrorCode.CHART_CONFIG_ERROR,
            details={
                'given': charts_json[:200],
                'error': str(e),
                'suggestion': '传入 JSON 数组，如: \'[{"type":"bar","x_axis":"city","y_axis":["revenue"]}]\'',
            },
        )
    if not isinstance(charts_cfg, list) or not charts_cfg:
        raise ChartError(
            "--charts 必须是非空 JSON 数组",
            ErrorCode.CHART_CONFIG_ERROR,
            details={
                'given_type': type(charts_cfg).__name__,
                'suggestion': '传入非空数组，每项形如 {"type":"line","title":"趋势","x_axis":"date","y_axis":["revenue"]}',
            },
        )
    for idx, cfg in enumerate(charts_cfg):
        if not isinstance(cfg, dict) or 'type' not in cfg:
            raise ChartError(
                f"--charts 第 {idx} 项必须是含 type 字段的对象",
                ErrorCode.CHART_CONFIG_ERROR,
                details={
                    'index': idx,
                    'given': str(cfg)[:200],
                    'suggestion': '每项形如 {"type":"bar","title":"标题","x_axis":"列名","y_axis":["列1","列2"]}',
                },
            )
    return charts_cfg


def main():
    if len(sys.argv) < 3:
        print("用法: python cli.py <file_path> <chart_type> [--title 标题] [--x-axis 列名] [--y-axis 列1 列2] [--transform-code 代码] [--output-dir 目录] [--skiprows N] [--header-row N] [--sheet <name|index>] [--lang zh|en] [--label-col 列名] [--color-by 列名] [--annotation 说明文字]\n"
              "      python cli.py <file_path> --charts '<JSON数组>' [--transform-code 代码] [--output-dir 目录] [--skiprows N] [--header-row N] [--sheet <name|index>] [--lang zh|en]\n"
              "      python cli.py <file_path> --charts-file <配置文件.json> [--transform-code 代码] [--output-dir 目录] [--skiprows N] [--header-row N] [--sheet <name|index>] [--lang zh|en]  # 推荐：transform 含中文/引号时避免 shell 转义问题")
        sys.exit(1)

    args = sys.argv[1:]
    file_path = args[0]

    title = None
    x_axis = None
    y_axis = None
    transform_code = None
    output_dir = './smart_charts_output'
    skiprows = None
    header_row = None
    sheet_name = 0
    lang = None
    label_col = None
    color_by = None
    annotation = None
    charts_json = None

    # 第 2 个位置参数是 chart_type；若它是 flag（如 --charts 多图模式）则不占位
    if args[1].startswith('--'):
        chart_type = None
        i = 1
    else:
        chart_type = args[1]
        i = 2
    while i < len(args):
        if args[i] == '--title' and i + 1 < len(args):
            title = args[i + 1]; i += 2
        elif args[i] == '--x-axis' and i + 1 < len(args):
            x_axis = args[i + 1]; i += 2
        elif args[i] == '--y-axis':
            y_list = []
            i += 1
            while i < len(args) and not args[i].startswith('--'):
                y_list.extend(args[i].split())
                i += 1
            y_axis = y_list if y_list else None
        elif args[i] == '--transform-code' and i + 1 < len(args):
            transform_code = args[i + 1]; i += 2
        elif args[i] == '--output-dir' and i + 1 < len(args):
            output_dir = args[i + 1]; i += 2
        elif args[i] == '--skiprows' and i + 1 < len(args):
            skiprows = int(args[i + 1]); i += 2
        elif args[i] == '--header-row' and i + 1 < len(args):
            header_row = int(args[i + 1]); i += 2
        elif args[i] == '--sheet' and i + 1 < len(args):
            v = args[i + 1]
            sheet_name = int(v) if v.lstrip('-').isdigit() else v
            i += 2
        elif args[i] == '--lang' and i + 1 < len(args):
            lang = args[i + 1]; i += 2
        elif args[i] == '--label-col' and i + 1 < len(args):
            label_col = args[i + 1]; i += 2
        elif args[i] == '--color-by' and i + 1 < len(args):
            color_by = args[i + 1]; i += 2
        elif args[i] == '--annotation' and i + 1 < len(args):
            annotation = args[i + 1]; i += 2
        elif args[i] == '--charts' and i + 1 < len(args):
            charts_json = args[i + 1]; i += 2
        elif args[i] == '--charts-file' and i + 1 < len(args):
            # A1: 从文件读取 --charts JSON，避免中文/引号在 shell 转义中损坏
            charts_file = args[i + 1]
            try:
                with open(charts_file, 'r', encoding='utf-8') as f:
                    charts_json = f.read()
            except OSError as e:
                print(json.dumps({
                    'error': f'无法读取 --charts-file: {e}',
                    'code': 1001,
                    'code_name': 'FILE_NOT_FOUND',
                    'details': {'given': charts_file, 'suggestion': '确认 JSON 配置文件路径正确且可读'},
                }, ensure_ascii=False), file=sys.stderr)
                sys.exit(1)
            i += 2
        else:
            i += 1

    try:
        dp = DataParser()
        df = dp.parse_file(file_path, skiprows=skiprows, header_row=header_row, sheet_name=sheet_name)
        gen = ChartGenerator(output_dir=output_dir)

        if charts_json is not None:
            # 多图模式：一次解析，批量生成；全局 transform 先应用，再执行各图配置
            charts_cfg = _parse_charts_json(charts_json)
            if transform_code:
                if __package__ is None:
                    from scripts.data_transformer import DataTransformer
                else:
                    from .data_transformer import DataTransformer
                df = DataTransformer().transform(df, transform_code)
            result = gen.generate_multi_charts(df, charts_cfg, lang=lang)
            items = result['charts']
            succeeded = sum(1 for c in items if c.get('success'))
            summary = {'total': len(items), 'succeeded': succeeded, 'failed': len(items) - succeeded}
            print(json.dumps({'charts': items, 'summary': summary}, ensure_ascii=False))
            if succeeded == 0:
                sys.exit(1)
        else:
            if chart_type is None:
                print(f"错误: 缺少图表类型参数（单图模式需 <chart_type>，多图模式需 --charts）", file=sys.stderr)
                sys.exit(1)
            result = gen.generate_chart(df, chart_type, title=title, x_axis=x_axis, y_axis=y_axis,
                                        transform_code=transform_code, lang=lang,
                                        label_col=label_col, color_by=color_by, annotation=annotation)
            print(json.dumps(result, ensure_ascii=False))
            if not result['chart']['success']:
                sys.exit(1)
    except SmartChartsError as e:
        print(json.dumps(e.to_dict(), ensure_ascii=False), file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"错误: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()

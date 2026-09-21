"""数据解析器。将 CSV、Excel、JSON、TXT 等文件解析为 DataFrame。"""

import sys
import pandas as pd
import numpy as np
import json
import re
from pathlib import Path
from typing import Dict, List, Any, Optional, Tuple, Union

if __name__ == '__main__' and __package__ is None:
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
    from scripts.exceptions import FileError, DataError, ErrorCode
else:
    from .exceptions import FileError, DataError, ErrorCode


class DataParser:

    MAX_FILE_SIZE_MB = 100  # 单文件最大允许大小（MB）

    def __init__(self):
        self._parsers = {
            '.csv': self._parse_csv,
            '.tsv': self._parse_tsv,
            '.xlsx': self._parse_excel,
            '.xls': self._parse_excel,
            '.json': self._parse_json,
            '.txt': self._parse_text,
        }

    def parse_file(
        self,
        file_path: str,
        skiprows: Optional[int] = None,
        header_row: Optional[int] = None,
        sheet_name: Union[int, str] = 0,
        **kwargs,
    ) -> pd.DataFrame:
        """解析单个文件。

        多行表头/前导冗余行处理（参数互斥，按需传一个即可）：
        - skiprows: 跳过前 N 行后再读取（pandas read_csv/read_excel 的 skiprows 语义）
        - header_row: 指定第 N 行作为列名（0-based），其上方行被丢弃，下方作为数据
        - sheet_name: Excel 工作表索引或名称（默认第 0 个）
        参数值由调用方根据实际数据决定，本技能不预设任何固定行数。
        """
        path = Path(file_path)
        if not path.exists():
            raise FileError(
                f"文件不存在: {file_path}",
                ErrorCode.FILE_NOT_FOUND,
                details={'path': file_path, 'suggestion': '检查路径是否正确，或使用绝对路径'},
            )
        if not path.is_file():
            raise FileError(
                f"不是文件: {file_path}",
                ErrorCode.FILE_NOT_REGULAR,
                details={'path': file_path, 'suggestion': '路径指向的不是常规文件（可能是目录）'},
            )
        if path.stat().st_size > self.MAX_FILE_SIZE_MB * 1024 * 1024:
            raise FileError(
                f"文件超过{self.MAX_FILE_SIZE_MB}MB限制",
                ErrorCode.FILE_SIZE_EXCEEDED,
                details={
                    'path': file_path,
                    'size_mb': round(path.stat().st_size / 1024 / 1024, 2),
                    'limit_mb': self.MAX_FILE_SIZE_MB,
                    'suggestion': f'拆分文件或筛选行/列后重试，单文件上限 {self.MAX_FILE_SIZE_MB}MB',
                },
            )

        ext = path.suffix.lower()
        if ext not in self._parsers:
            ext = self._detect_type(path)
        if ext not in self._parsers:
            supported = list(self._parsers.keys())
            raise FileError(
                f"不支持的格式: {ext}，支持: {supported}",
                ErrorCode.FILE_FORMAT_INVALID,
                details={
                    'path': file_path,
                    'given_ext': ext,
                    'supported': supported,
                    'suggestion': '将文件转为支持的格式（CSV/Excel/JSON）后重试',
                },
            )

        # 统一把表头清洗参数塞进 kwargs，每个 _parse_* 按需读取
        if skiprows is not None:
            kwargs['skiprows'] = skiprows
        if header_row is not None:
            kwargs['header_row'] = header_row
        kwargs.setdefault('sheet_name', sheet_name)

        df = self._parsers[ext](path, **kwargs)
        if df.empty:
            raise DataError(
                "文件内容为空",
                ErrorCode.DATA_EMPTY,
                details={'path': file_path, 'suggestion': '检查文件是否只有表头无数据行'},
            )

        df = self._clean(df)
        self._validate(df)
        return df

    def parse_files(self, file_paths: List[str], merge: bool = False) -> Dict[str, Any]:
        """解析多个文件，返回统一结构 {'merged': bool, 'data': ..., 'merge_type': Optional[str]}。

        - merge=False: data 为 List[Dict[str, pd.DataFrame]]，每项含 {'file', 'data'}
        - merge=True:  data 为合并后的 pd.DataFrame，merge_type 描述合并方式
        """
        results = []
        for fp in file_paths:
            df = self.parse_file(fp)
            results.append({'file': Path(fp).name, 'data': df})

        if not merge:
            return {'merged': False, 'data': results, 'merge_type': None}

        merged_df, merge_type = self._merge(results)
        return {'merged': True, 'data': merged_df, 'merge_type': merge_type}

    def get_data_summary(self, df: pd.DataFrame) -> Dict[str, Any]:
        summary: Dict[str, Any] = {
            'shape': list(df.shape),
            'columns': list(df.columns),
            'dtypes': {col: str(dtype) for col, dtype in df.dtypes.items()},
            'missing': {k: int(v) for k, v in df.isnull().sum().to_dict().items()},
            'sample': df.head(5).to_dict('records'),
        }
        numeric_cols = df.select_dtypes(include=[np.number]).columns
        if len(numeric_cols) > 0:
            summary['stats'] = df[numeric_cols].describe().to_dict()
        return summary

    def _merge(self, results: List[Dict]) -> Tuple[pd.DataFrame, str]:
        """尝试合并多个 DataFrame。返回 (merged_df, merge_type)。"""
        dfs = [r['data'] for r in results]
        col_sets = [set(df.columns) for df in dfs]

        # 纵向拼接：所有文件列名完全相同
        if all(len(cs) > 0 for cs in col_sets) and all(cs == col_sets[0] for cs in col_sets):
            merged = pd.concat(dfs, ignore_index=True)
            merged['source_file'] = [r['file'] for r in results for _ in range(len(r['data']))]
            cols = list(merged.columns)
            cols.remove('source_file')
            merged = merged[cols + ['source_file']]
            return merged, '纵向拼接'

        # 横向关联：公共列占比 >= 50%
        if len(dfs) >= 2 and all(len(cs) > 0 for cs in col_sets):
            intersection = col_sets[0]
            for cs in col_sets[1:]:
                intersection = intersection & cs
            avg_col_count = sum(len(cs) for cs in col_sets) / len(col_sets)
            if len(intersection) >= avg_col_count * 0.5:
                merged = dfs[0]
                for df in dfs[1:]:
                    merged = pd.merge(merged, df, on=list(intersection), how='outer', suffixes=('', '_dup'))
                merged.columns = [c.replace('_dup', '') if c.endswith('_dup') else c for c in merged.columns]
                merged = merged.loc[:, ~merged.columns.duplicated()]
                return merged, '横向关联'

        # 无法自动合并
        summary_parts = []
        for r in results:
            summary_parts.append(f"{r['file']}: {r['data'].shape[0]}行 {r['data'].shape[1]}列, 列={list(r['data'].columns)}")
        raise DataError(
            f"文件结构差异大，无法自动合并。各文件信息：\n" + "\n".join(summary_parts) +
            "\n请指定关联方式，或分别分析。",
            ErrorCode.DATA_PARSE_ERROR,
            details={
                'files': [{'file': r['file'], 'shape': list(r['data'].shape), 'columns': list(r['data'].columns)} for r in results],
                'suggestion': '请指定关联方式（如 merge_files 时提供 on 列），或对每个文件分别分析',
            },
        )

    # ---- 解析实现 ----

    def _parse_csv(self, path: Path, **kw) -> pd.DataFrame:
        encoding = kw.get('encoding', 'utf-8')
        read_kwargs = self._build_header_kwargs(kw)
        try:
            return pd.read_csv(path, encoding=encoding, low_memory=False, **read_kwargs)
        except pd.errors.EmptyDataError:
            raise DataError(
                "文件内容为空",
                ErrorCode.DATA_EMPTY,
                details={'path': str(path), 'suggestion': '检查文件是否只有表头无数据行'},
            )
        except pd.errors.ParserError as e:
            raise DataError(
                f"CSV 解析失败: {e}",
                ErrorCode.DATA_PARSE_ERROR,
                details={
                    'path': str(path),
                    'error': str(e),
                    'suggestion': '文件可能含前导说明行或列数不一致，先不加参数运行查看原始布局，再用 --skiprows N 或 --header-row N 跳过冗余行',
                },
            )
        except UnicodeDecodeError:
            for enc in ('gbk', 'gb2312', 'utf-16', 'latin1'):
                try:
                    return pd.read_csv(path, encoding=enc, low_memory=False, **read_kwargs)
                except (UnicodeDecodeError, pd.errors.EmptyDataError):
                    continue
            raise DataError(
                "无法解码文件",
                ErrorCode.DATA_PARSE_ERROR,
                details={'path': str(path), 'tried_encodings': ['utf-8', 'gbk', 'gb2312', 'utf-16', 'latin1'], 'suggestion': '用文本编辑器另存为 UTF-8 后重试'},
            )

    def _parse_tsv(self, path: Path, **kw) -> pd.DataFrame:
        """解析 TSV 文件，支持多编码回退（与 CSV 一致）。"""
        encoding = kw.get('encoding', 'utf-8')
        read_kwargs = self._build_header_kwargs(kw)
        try:
            return pd.read_csv(path, sep='\t', encoding=encoding, low_memory=False, **read_kwargs)
        except pd.errors.EmptyDataError:
            raise DataError(
                "文件内容为空",
                ErrorCode.DATA_EMPTY,
                details={'path': str(path), 'suggestion': '检查文件是否只有表头无数据行'},
            )
        except pd.errors.ParserError as e:
            raise DataError(
                f"TSV 解析失败: {e}",
                ErrorCode.DATA_PARSE_ERROR,
                details={
                    'path': str(path),
                    'error': str(e),
                    'suggestion': '文件可能含前导说明行或列数不一致，先不加参数运行查看原始布局，再用 --skiprows N 或 --header-row N 跳过冗余行',
                },
            )
        except UnicodeDecodeError:
            for enc in ('gbk', 'gb2312', 'utf-16', 'latin1'):
                try:
                    return pd.read_csv(path, sep='\t', encoding=enc, low_memory=False, **read_kwargs)
                except (UnicodeDecodeError, pd.errors.EmptyDataError):
                    continue
            raise DataError(
                "无法解码文件",
                ErrorCode.DATA_PARSE_ERROR,
                details={'path': str(path), 'tried_encodings': ['utf-8', 'gbk', 'gb2312', 'utf-16', 'latin1'], 'suggestion': '用文本编辑器另存为 UTF-8 后重试'},
            )

    def _parse_excel(self, path: Path, **kw) -> pd.DataFrame:
        sheet = kw.get('sheet_name', 0)
        # 根据扩展名选择引擎：.xlsx 用 openpyxl，.xls 用 xlrd
        ext = path.suffix.lower()
        engine = 'xlrd' if ext == '.xls' else 'openpyxl'
        read_kwargs = self._build_header_kwargs(kw)
        try:
            df = pd.read_excel(path, sheet_name=sheet, engine=engine, **read_kwargs)
            if df.empty:
                xl = pd.ExcelFile(path, engine=engine)
                for s in xl.sheet_names:
                    df = pd.read_excel(path, sheet_name=s, engine=engine, **read_kwargs)
                    if not df.empty:
                        return df
            return df
        except Exception as e:
            raise DataError(
                f"Excel读取失败: {e}",
                ErrorCode.DATA_PARSE_ERROR,
                details={'path': str(path), 'engine': engine, 'error': str(e), 'suggestion': '检查文件是否损坏、是否为真正的 Excel 文件（非改扩展名的 CSV）'},
            )

    @staticmethod
    def _build_header_kwargs(kw: Dict[str, Any]) -> Dict[str, Any]:
        """从调用 kwargs 中提取表头清洗参数，转为 pandas read_csv/read_excel 接受的形式。

        - skiprows: 整数 N，跳过前 N 行
        - header_row: 整数 N（0-based），将第 N 行作为列名，丢弃其上方行
        两者互斥；同时给出时以 header_row 优先（更精确）。
        """
        out: Dict[str, Any] = {}
        skiprows = kw.get('skiprows')
        header_row = kw.get('header_row')
        if header_row is not None:
            # header=N 等价于：第 N 行作为列名，前面行被 pandas 自动跳过
            out['header'] = int(header_row)
        elif skiprows is not None:
            out['skiprows'] = int(skiprows)
        return out

    def _parse_json(self, path: Path, **kw) -> pd.DataFrame:
        """解析 JSON 文件，支持多编码回退。"""
        data = None
        last_error = None
        for enc in ('utf-8', 'utf-8-sig', 'gbk', 'gb2312', 'latin1'):
            try:
                with open(path, 'r', encoding=enc) as f:
                    data = json.load(f)
                break
            except UnicodeDecodeError:
                continue
            except json.JSONDecodeError as e:
                last_error = e
                continue
        if data is None:
            if last_error:
                raise DataError(
                    f"JSON 解析失败: {last_error}",
                    ErrorCode.DATA_PARSE_ERROR,
                    details={'path': str(path), 'error': str(last_error), 'suggestion': '用 JSON 校验工具检查格式（如 jsonlint.com）'},
                )
            raise DataError(
                "无法解码 JSON 文件",
                ErrorCode.DATA_PARSE_ERROR,
                details={'path': str(path), 'tried_encodings': ['utf-8', 'utf-8-sig', 'gbk', 'gb2312', 'latin1'], 'suggestion': '用文本编辑器另存为 UTF-8 后重试'},
            )

        # 提取待转表的记录集，并检测字段值是否含 dict/list（即嵌套超过 1 层）
        if isinstance(data, list):
            records = data
        elif isinstance(data, dict):
            list_value = next((v for v in data.values() if isinstance(v, list)), None)
            records = list_value if list_value is not None else [data]
        else:
            raise DataError(
                "不支持的JSON结构",
                ErrorCode.DATA_PARSE_ERROR,
                details={'path': str(path), 'top_level_type': type(data).__name__,
                         'suggestion': 'JSON 顶层必须是数组、或含数组值的对象'},
            )

        for r in records:
            if isinstance(r, dict) and any(isinstance(v, (dict, list)) for v in r.values()):
                raise DataError(
                    "JSON 嵌套超过 1 层，暂不支持",
                    ErrorCode.DATA_PARSE_ERROR,
                    details={'path': str(path),
                             'suggestion': '请简化 JSON 结构（每条记录的字段值只能是标量），或用工具先转为 CSV 再生成图表'},
                )

        if isinstance(data, list):
            return pd.DataFrame(data)
        if list_value is not None:
            return pd.DataFrame(list_value)
        return pd.json_normalize(data)

    def _parse_text(self, path: Path, **kw) -> pd.DataFrame:
        """解析文本文件，支持多编码回退。"""
        lines = None
        detected_enc = 'utf-8'
        for enc in ('utf-8', 'gbk', 'gb2312', 'latin1'):
            try:
                with open(path, 'r', encoding=enc) as f:
                    lines = [l.strip() for l in f if l.strip()]
                detected_enc = enc
                break
            except UnicodeDecodeError:
                continue
        if not lines:
            raise DataError(
                "文件为空",
                ErrorCode.DATA_EMPTY,
                details={'path': str(path), 'suggestion': '检查文件是否有内容'},
            )
        read_kwargs = self._build_header_kwargs(kw)
        for delim in (',', '\t', ';', '|'):
            if delim in lines[0] and len(lines[0].split(delim)) > 1:
                return pd.read_csv(path, sep=delim, encoding=detected_enc, low_memory=False, **read_kwargs)
        return pd.DataFrame({'content': lines})

    def _detect_type(self, path: Path) -> str:
        try:
            header = path.read_bytes(1024)
            text = header.decode('utf-8', errors='ignore')
            if text.strip().startswith(('{', '[')):
                return '.json'
            if header.startswith(b'\x50\x4B\x03\x04'):
                return '.xlsx'
            for delim in (',', '\t', ';'):
                if delim in text:
                    return '.csv'
        except Exception:
            pass
        return path.suffix.lower()

    def _clean(self, df: pd.DataFrame) -> pd.DataFrame:
        df = df.dropna(axis=1, how='all').dropna(axis=0, how='all').reset_index(drop=True)
        df.columns = [self._normalize_col(c) for c in df.columns]
        for col in df.columns:
            if df[col].dtype == 'object' or pd.api.types.is_string_dtype(df[col]):
                df[col] = self._to_numeric_if_possible(df[col])
        return df

    @staticmethod
    def _to_numeric_if_possible(series: pd.Series) -> pd.Series:
        """尝试将字符串列转换为数值列。

        先清理常见数值格式（货币符号/千分位逗号/百分号），
        再尝试整体转换（等价于已弃用的 errors='ignore' 语义：全列可转才转），
        百分号整体转为小数。避免使用 errors='ignore'，消除 FutureWarning。
        """
        s = series.astype(str).str.strip()
        if s.eq('').all():
            return series
        cleaned = s.str.replace(r'[¥$€£￥]', '', regex=True).str.replace(',', '', regex=False)
        has_pct = cleaned.str.endswith('%')
        cleaned = cleaned.str.rstrip('%')
        try:
            converted = pd.to_numeric(cleaned, errors='raise')
        except (ValueError, TypeError):
            return series
        if has_pct.any():
            converted = converted.where(~has_pct, converted / 100.0)
        return converted

    @staticmethod
    def _normalize_col(name: Any) -> str:
        if pd.isna(name):
            return 'unnamed'
        s = re.sub(r'[^\w\s]', '_', str(name).strip())
        s = re.sub(r'[\s_]+', '_', s).strip('_').lower()
        return s or 'unnamed'

    @staticmethod
    def _validate(df: pd.DataFrame):
        if df.empty:
            raise DataError(
                "数据为空",
                ErrorCode.DATA_EMPTY,
                details={'suggestion': '清洗后数据为空，检查原始数据是否全为空行/空列'},
            )


if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("用法: python data_parser.py <file1> [file2 ...] [--summary] [--merge] [--skiprows N] [--header-row N] [--sheet <name|index>]")
        sys.exit(1)

    # 单文件清洗参数（仅对单文件模式生效；多文件场景请在 transform_code 阶段处理）
    skiprows = None
    header_row = None
    sheet_name = 0

    args = sys.argv[1:]
    positional = []
    i = 0
    while i < len(args):
        a = args[i]
        if a == '--skiprows' and i + 1 < len(args):
            try:
                skiprows = int(args[i + 1])
            except ValueError:
                print(json.dumps({'error': '--skiprows 需要整数', 'code': 2001, 'code_name': 'DATA_PARSE_ERROR'}, ensure_ascii=False), file=sys.stderr)
                sys.exit(1)
            i += 2
        elif a == '--header-row' and i + 1 < len(args):
            try:
                header_row = int(args[i + 1])
            except ValueError:
                print(json.dumps({'error': '--header-row 需要整数', 'code': 2001, 'code_name': 'DATA_PARSE_ERROR'}, ensure_ascii=False), file=sys.stderr)
                sys.exit(1)
            i += 2
        elif a == '--sheet' and i + 1 < len(args):
            v = args[i + 1]
            sheet_name = int(v) if v.lstrip('-').isdigit() else v
            i += 2
        else:
            positional.append(a)
            i += 1

    do_summary = '--summary' in positional
    do_merge = '--merge' in positional
    paths = [p for p in positional if not p.startswith('--')]
    parser = DataParser()

    try:
        if len(paths) == 1 and not do_merge:
            df = parser.parse_file(
                paths[0],
                skiprows=skiprows,
                header_row=header_row,
                sheet_name=sheet_name,
            )
            if do_summary:
                print(json.dumps(parser.get_data_summary(df), ensure_ascii=False, indent=2, default=str))
            else:
                print(f"解析成功: {df.shape[0]} 行, {df.shape[1]} 列")
                print(f"列名: {list(df.columns)}")
                print(df.head(5).to_string())
        else:
            result = parser.parse_files(paths, merge=do_merge)
            if result['merged']:
                merged_df = result['data']
                merge_type = result['merge_type']
                if do_summary:
                    # summary 模式 stdout 必须是纯 JSON（agent 机器可读），merge_type 放入 JSON 内
                    summary = parser.get_data_summary(merged_df)
                    summary['merge_type'] = merge_type
                    print(json.dumps(summary, ensure_ascii=False, indent=2, default=str))
                else:
                    print(f"合并方式: {merge_type}")
                    print(f"合并后: {merged_df.shape[0]} 行, {merged_df.shape[1]} 列")
                    print(f"列名: {list(merged_df.columns)}")
                    print(merged_df.head(5).to_string())
            else:
                items = result['data']
                summaries = []
                for item in items:
                    s = parser.get_data_summary(item['data'])
                    summaries.append({'file': item['file'], **s})
                if do_summary:
                    print(json.dumps(summaries, ensure_ascii=False, indent=2, default=str))
                else:
                    for item in items:
                        df = item['data']
                        print(f"\n--- {item['file']}: {df.shape[0]} 行, {df.shape[1]} 列 ---")
                        print(f"列名: {list(df.columns)}")
                        print(df.head(3).to_string())
    except (FileError, DataError) as e:
        print(json.dumps(e.to_dict(), ensure_ascii=False), file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"错误: {e}", file=sys.stderr)
        sys.exit(1)

from __future__ import annotations

import json
import re
from typing import Any, Dict, List, Optional
from urllib.parse import urlencode

from lazyllm.tools.sql.sql_manager import SqlManager

from lazymind.chat.engine.tools.infra import get_core_api, handle_tool_errors, tool_error, tool_success

_DANGEROUS_SQL = re.compile(
    r'\b(insert|update|delete|drop|alter|truncate|create|replace|merge|grant|revoke|upsert|call|execute)\b',
    re.IGNORECASE,
)


def _strip_sql_comments(sql: str) -> str:
    sql = re.sub(r'/\*.*?\*/', '', sql, flags=re.DOTALL)
    return re.sub(r'--.*$', '', sql, flags=re.MULTILINE)


def _mask_sql_strings(sql: str) -> str:
    out: List[str] = []
    quote = ''
    index = 0
    while index < len(sql):
        ch = sql[index]
        if quote:
            if ch == quote:
                if index + 1 < len(sql) and sql[index + 1] == quote:
                    index += 2
                    continue
                quote = ''
            index += 1
            continue
        if ch in ("'", '"'):
            quote = ch
            out.append(' ')
            index += 1
            continue
        out.append(ch)
        index += 1
    return ''.join(out)


def _validate_readonly_sql(sql: str) -> str:
    statement = str(sql or '').strip()
    if not statement:
        return ''
    cleaned = _mask_sql_strings(_strip_sql_comments(statement)).strip()
    if cleaned.endswith(';'):
        cleaned = cleaned[:-1].strip()
    if ';' in cleaned:
        return ''
    if not re.match(r'^(select|with)\b', cleaned, re.IGNORECASE):
        return ''
    if _DANGEROUS_SQL.search(cleaned):
        return ''
    return statement


def _options_str(options: Any) -> Optional[str]:
    if not isinstance(options, dict) or not options:
        return None
    values = {str(key): str(value) for key, value in options.items() if str(key).strip()}
    return urlencode(values) if values else None


class ReadOnlySqlManager(SqlManager):
    def _ensure_database_exists(self, conn_url: str):  # noqa: ARG002
        return

    def execute_readonly_query(self, statement: str) -> Any:
        sql = _validate_readonly_sql(statement)
        if not sql:
            raise ValueError('Only one read-only SELECT/WITH statement is allowed')
        raw = self.execute_query(sql)
        try:
            return json.loads(raw)
        except ValueError:
            return raw


class ExternalDBToolGroup:
    """Read-only tools for querying user-configured external relational databases."""

    __public_apis__ = ['list_external_dbs', 'describe_external_db', 'external_db_query']

    @handle_tool_errors
    def list_external_dbs(self) -> Dict[str, Any]:
        """List external database connections available to the current user."""
        return tool_success('list_external_dbs', get_core_api('/data-sources/database-connections'))

    def _connection(self, connection_id: str) -> Dict[str, Any]:
        connection_id = str(connection_id or '').strip()
        if not connection_id:
            raise ValueError('connection_id is required')
        return get_core_api(f'/data-sources/database-connections/{connection_id}:secret')

    def _manager(self, connection_id: str) -> ReadOnlySqlManager:
        conn = self._connection(connection_id)
        db_type = str(conn.get('db_type') or '').strip().lower()
        if db_type == 'postgres':
            db_type = 'postgresql'
        return ReadOnlySqlManager(
            db_type=db_type,
            user=str(conn.get('username') or ''),
            password=str(conn.get('password') or ''),
            host=str(conn.get('host') or ''),
            port=int(conn.get('port') or 0),
            db_name=str(conn.get('database_name') or ''),
            options_str=_options_str(conn.get('options')),
        )

    @handle_tool_errors
    def describe_external_db(self, connection_id: str) -> Dict[str, Any]:
        """Inspect table schema before generating SQL for a configured external database connection."""
        manager = self._manager(connection_id)
        try:
            tables = manager.visible_tables
            return tool_success('describe_external_db', {'tables': tables, 'schema': manager.desc})
        finally:
            manager.dispose()

    @handle_tool_errors
    def external_db_query(self, connection_id: str, sql: str) -> Dict[str, Any]:
        """Execute one Agent-generated read-only SELECT/WITH SQL statement on a configured external database."""
        readonly_sql = _validate_readonly_sql(sql)
        if not readonly_sql:
            return tool_error(
                'external_db_query',
                'Only one read-only SELECT/WITH SQL statement is allowed.',
                error_type='ReadOnlySQLRejected',
            )
        manager = self._manager(connection_id)
        try:
            rows = manager.execute_readonly_query(readonly_sql)
            if isinstance(rows, list) and len(rows) > 200:
                rows = rows[:200]
            return tool_success('external_db_query', {'sql': readonly_sql, 'rows': rows})
        finally:
            manager.dispose()

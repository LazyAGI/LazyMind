import json
from typing import Any

from lazyllm.tools.sql.sql_manager import SqlManager

from .external_db import _validate_readonly_sql


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

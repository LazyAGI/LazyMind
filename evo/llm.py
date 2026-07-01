from __future__ import annotations

from collections.abc import Mapping
import os
from typing import Any


class LazyLLMClient:
    def __init__(self, *, llm_config: Mapping[str, Any] | None = None, model: str | None = None) -> None:
        self.llm_config = dict(llm_config or {})
        self.model = _model_role(self.llm_config, model)
        self.session_id = f'evo-llm-{id(self)}'
        self._llm: Any | None = None

    def __call__(self, prompt: str, **kwargs: Any) -> Any:
        _activate_session(self.session_id, self.llm_config)
        if self._llm is None:
            self._llm = _lazyllm_model(self.model)
        return self._llm(prompt, **kwargs)


def _lazyllm_model(model: str) -> Any:
    from lazyllm import AutoModel

    return AutoModel(model=model)


def _activate_session(session_id: str, llm_config: Mapping[str, Any]) -> None:
    import lazyllm

    from lazymind.model_config import inject_model_config

    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(session_id)
    if llm_config:
        inject_model_config(dict(llm_config))


def _model_role(llm_config: Mapping[str, Any], model: str | None) -> str:
    preferred = str(model or os.getenv('LAZYMIND_EVO_LLM_ROLE') or 'evo_llm').strip() or 'evo_llm'
    if model or not llm_config or preferred in llm_config:
        return preferred
    return 'llm' if 'llm' in llm_config else preferred

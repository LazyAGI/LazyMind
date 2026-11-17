"""Carry run cancellation into model requests, independently of model fallback."""
from __future__ import annotations

import copy
from dataclasses import replace

from lazyllm.module import ModuleBase
from lazyllm.module.llms.onlinemodule.base import ModelCallControl
from lazyllm.module.servermodule import LLMBase

from .cancellation import UserCancelledError


class CancellableLLM(ModuleBase, LLMBase):
    def __init__(self, llm, *, cancel_check):
        ModuleBase.__init__(self)
        LLMBase.__init__(self, stream=llm.stream, init_prompt=False, type=llm.type,
                         static_params=copy.deepcopy(llm._static_params))
        self._llm = llm
        self._cancel_check = cancel_check
        self._steering_interrupt_check = None
        self._prompt = copy.copy(llm._prompt)
        self._formatter = copy.copy(llm._formatter)

    def forward(self, *args, call_control=None, **kwargs):
        def check_cancel():
            if self._cancel_check(None):
                raise UserCancelledError('stopped during a model request')
            if self._steering_interrupt_check is not None:
                self._steering_interrupt_check()
            return bool(call_control and call_control.cancel_check and call_control.cancel_check())

        control = replace(call_control or ModelCallControl(), cancel_check=check_cancel)
        control.check()
        view = self._llm.share(prompt=self._prompt, format=self._formatter, stream=self._stream)
        view._static_params = self._static_params
        view.used_by(self._used_by_moduleid or self._module_id)
        result = view(*args, call_control=control, **kwargs)
        control.check()
        return result

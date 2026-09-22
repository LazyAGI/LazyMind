"""Track capability use for settings confirmations, without arguments or results."""
from __future__ import annotations

from contextlib import contextmanager
import threading
import uuid

from lazymind.chat.engine.tools.infra.core_api_client import post_core_api


class SettingsActivity:
    def __init__(self, user_id: str, conversation_id: str, *, skill_manager=None, skill_root=None):
        self.user_id = user_id
        self.conversation_id = conversation_id
        self._skill_manager = skill_manager
        self._skill_root = skill_root
        self._run_id = uuid.uuid4().hex
        self._entries = {}
        self._lock = threading.RLock()
        self._stop = threading.Event()
        self._thread = None
        self._skill_id = None
        self._active_skills = 0
        self._closing = False

    def _report(self, entry, active):
        post_core_api('/internal/settings/activity', {
            **entry, 'run_id': self._run_id, 'conversation_id': self.conversation_id, 'active': active,
        }, user_id=self.user_id)

    def _start(self, capability, resource_id=''):
        entry = {'id': uuid.uuid4().hex, 'capability': capability, 'resource_id': resource_id}
        # Start is synchronous: a settings check must never miss a dispatched call.
        try:
            self._report(entry, True)
        except Exception:
            raise RuntimeError('Unable to register active capability use. Please retry.') from None
        with self._lock:
            self._entries[entry['id']] = entry
            if self._thread is None:
                self._thread = threading.Thread(target=self._heartbeat, daemon=True)
                self._thread.start()
        return entry['id']

    def _finish(self, identifier):
        with self._lock:
            entry = self._entries.pop(identifier, None)
            if self._closing and not self._entries:
                self._stop.set()
        if entry:
            try:
                self._report(entry, False)
            except Exception:
                # Core treats an expired record in a live run as unknown, not idle.
                pass

    def _heartbeat(self):
        while not self._stop.wait(15):
            # Keep removal ordered after heartbeat so a finished call cannot reappear.
            with self._lock:
                for entry in self._entries.values():
                    try:
                        self._report(entry, True)
                    except Exception:
                        pass

    def _managed_skill(self, prepared):
        if prepared.tool_source != 'skill' or prepared.tool_name not in {'get_skill', 'run_script', 'read_reference'}:
            return False
        if self._skill_root is None:
            return True
        if not self._skill_manager or not self._skill_root:
            return False
        info, error = self._skill_manager._get_visible_skill_info(prepared.arguments.get('name', ''))
        if error or not info:
            return False
        path = str(info.get('path') or '').rstrip('/')
        root = str(self._skill_root).rstrip('/')
        return path == root or path.startswith(root + '/')

    @contextmanager
    def tool_scope(self, prepared):
        if not self.user_id or not self.conversation_id:
            yield
            return
        identifier = None
        skill = self._managed_skill(prepared)
        if prepared.tool_source == 'mcp':
            identifier = self._start('mcp_enabled', prepared.tool_origin)
        elif skill:
            # A loaded skill remains in this run's context until the run ends.
            with self._lock:
                if self._skill_id is None:
                    self._skill_id = self._start('skills_enabled')
                self._active_skills += 1
        try:
            yield
        finally:
            if identifier:
                self._finish(identifier)
            elif skill:
                with self._lock:
                    self._active_skills -= 1
                    if self._closing and self._active_skills == 0:
                        self._finish(self._skill_id)

    def close(self):
        with self._lock:
            self._closing = True
            if self._skill_id and self._active_skills == 0:
                self._finish(self._skill_id)
            if not self._entries:
                self._stop.set()
        # A canceled stream can leave a dispatched tool running. Its scope owns
        # removal, and heartbeats continue until that call actually returns.

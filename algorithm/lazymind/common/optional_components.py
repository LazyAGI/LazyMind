"""Desktop capability gates; cloud and existing full runtimes remain enabled."""

import os


class ComponentRequiredError(RuntimeError):
    code = 'CAPABILITY_REQUIRED'

    def __init__(self, component):
        self.component = component
        self.settings_url = f'/settings?section=system_tools#python-{component}-dependency'
        title = '本地知识库与文档解析'
        super().__init__(f'请先在设置 → 系统工具 → 依赖中安装“{title}”，然后重启本地服务。'
                         f' {self.settings_url}')


def component_enabled(component):
    return os.environ.get(f'LAZYMIND_{component.upper()}_ENABLED', '1').strip().lower() not in {'0', 'false'}


def require_component(component):
    if not component_enabled(component):
        raise ComponentRequiredError(component)

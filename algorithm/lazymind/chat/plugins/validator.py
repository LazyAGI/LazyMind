from __future__ import annotations

import os
from dataclasses import dataclass, field


@dataclass
class ValidationResult:
    errors: list[str] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)
    infos: list[str] = field(default_factory=list)

    @property
    def is_valid(self) -> bool:
        return len(self.errors) == 0

    def merge(self, other: 'ValidationResult') -> None:
        self.errors.extend(other.errors)
        self.warnings.extend(other.warnings)
        self.infos.extend(other.infos)


def validate_state_yml(state_yml: dict, plugin_yaml: dict) -> ValidationResult:
    result = ValidationResult()
    declared_steps = {s['id'] for s in plugin_yaml.get('steps', [])}
    initial = state_yml.get('initial', '')

    if not initial:
        result.errors.append('state.yml: missing "initial" field')
    elif initial not in declared_steps:
        result.errors.append(
            f'state.yml: initial step "{initial}" is not declared in plugin.yaml steps'
        )

    transitions = state_yml.get('transitions', {})
    for from_step, edges in transitions.items():
        if from_step not in declared_steps:
            result.warnings.append(
                f'state.yml transitions: "{from_step}" is not declared in plugin.yaml steps'
            )
        if not isinstance(edges, list):
            result.errors.append(
                f'state.yml transitions["{from_step}"]: expected a list of {{to, condition}} dicts'
            )
            continue
        for edge in edges:
            to_step = edge.get('to', '')
            if to_step not in declared_steps:
                result.errors.append(
                    f'state.yml transitions["{from_step}"] -> "{to_step}": '
                    f'target step is not declared in plugin.yaml steps'
                )

    state_steps = set(state_yml.get('steps', {}).keys())
    for sid in declared_steps - state_steps:
        result.infos.append(f'state.yml: step "{sid}" has no execution spec in state.yml steps section')
    for sid in state_steps - declared_steps:
        result.warnings.append(f'state.yml: step "{sid}" in steps section is not declared in plugin.yaml')

    return result


def validate_consistency(state_yml: dict, scenario_md: str) -> ValidationResult:
    result = ValidationResult()
    step_ids = list(state_yml.get('steps', {}).keys())
    for step_id in step_ids:
        if step_id not in scenario_md:
            result.warnings.append(
                f'consistency check: step "{step_id}" from state.yml not found in scenario.md text'
            )
    return result


def validate_driver_mode(plugin_yaml: dict, driver_md_exists: bool) -> ValidationResult:
    result = ValidationResult()
    if driver_md_exists:
        return result
    for step in plugin_yaml.get('steps', []):
        if step.get('default_mode', 'human') == 'auto':
            result.errors.append(
                f'Plugin "{plugin_yaml.get("id", "?")}" step "{step["id"]}" uses default_mode=auto '
                f'but driver.md does not exist. Provide driver.md or set default_mode=human.'
            )
    return result


def validate_all(plugin_dir: str) -> ValidationResult:
    result = ValidationResult()

    plugin_yaml_path = os.path.join(plugin_dir, 'plugin.yaml')
    if not os.path.exists(plugin_yaml_path):
        result.errors.append(f'plugin.yaml not found in {plugin_dir}')
        return result

    import yaml
    with open(plugin_yaml_path, encoding='utf-8') as f:
        try:
            plugin_yaml = yaml.safe_load(f) or {}
        except Exception as e:
            result.errors.append(f'plugin.yaml parse error: {e}')
            return result

    scenario_path = os.path.join(plugin_dir, 'scenario', 'scenario.md')
    if not os.path.exists(scenario_path):
        result.errors.append(f'scenario/scenario.md not found in {plugin_dir}')
        return result

    with open(scenario_path, encoding='utf-8') as f:
        scenario_md = f.read()

    driver_path = os.path.join(plugin_dir, 'scenario', 'driver.md')
    driver_md_exists = os.path.exists(driver_path)

    result.merge(validate_driver_mode(plugin_yaml, driver_md_exists))
    if not result.is_valid:
        return result

    state_yml_path = os.path.join(plugin_dir, 'scenario', 'state.yml')
    if os.path.exists(state_yml_path):
        with open(state_yml_path, encoding='utf-8') as f:
            try:
                state_yml = yaml.safe_load(f) or {}
            except Exception as e:
                result.errors.append(f'state.yml parse error: {e}')
                return result
        result.merge(validate_state_yml(state_yml, plugin_yaml))
        result.merge(validate_consistency(state_yml, scenario_md))

    return result

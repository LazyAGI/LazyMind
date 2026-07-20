from __future__ import annotations

from typing import Optional

from .common import (
    optional_str,
    optional_str_list,
    parse_yaml_frontmatter,
    reject_unknown_keys,
    require_mapping,
    require_no_body,
)

_ROOT_KEYS = {'schema_version', 'identity', 'locale', 'professional', 'accessibility'}
_IDENTITY_KEYS = {'preferred_name', 'aliases', 'pronouns'}
_LOCALE_KEYS = {'languages', 'timezone', 'region'}
_PROFESSIONAL_KEYS = {'roles', 'organization', 'industry', 'expertise_domains'}
_ACCESSIBILITY_KEYS = {'communication_needs'}


def validate_profile_content(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'profile requires non-empty YAML frontmatter content.'

    frontmatter, body = parse_yaml_frontmatter(content)
    if not frontmatter:
        return 'profile must contain YAML frontmatter.'

    body_error = require_no_body(body, entity='profile')
    if body_error:
        return body_error

    root_error = reject_unknown_keys(frontmatter, _ROOT_KEYS, field='profile')
    if root_error:
        return root_error

    if 'schema_version' not in frontmatter:
        return "profile requires 'schema_version'."
    if frontmatter.get('schema_version') != 1:
        return "profile 'schema_version' must be 1."

    identity = frontmatter.get('identity')
    if identity is not None:
        err = require_mapping(identity, field='identity')
        if err:
            return err
        assert isinstance(identity, dict)
        err = reject_unknown_keys(identity, _IDENTITY_KEYS, field='identity')
        if err:
            return err
        for key in ('preferred_name', 'pronouns'):
            err = optional_str(identity.get(key), field=f'identity.{key}')
            if err:
                return err
        err = optional_str_list(identity.get('aliases'), field='identity.aliases')
        if err:
            return err

    locale = frontmatter.get('locale')
    if locale is not None:
        err = require_mapping(locale, field='locale')
        if err:
            return err
        assert isinstance(locale, dict)
        err = reject_unknown_keys(locale, _LOCALE_KEYS, field='locale')
        if err:
            return err
        err = optional_str_list(locale.get('languages'), field='locale.languages')
        if err:
            return err
        for key in ('timezone', 'region'):
            err = optional_str(locale.get(key), field=f'locale.{key}')
            if err:
                return err

    professional = frontmatter.get('professional')
    if professional is not None:
        err = require_mapping(professional, field='professional')
        if err:
            return err
        assert isinstance(professional, dict)
        err = reject_unknown_keys(professional, _PROFESSIONAL_KEYS, field='professional')
        if err:
            return err
        for key in ('roles', 'expertise_domains'):
            err = optional_str_list(professional.get(key), field=f'professional.{key}')
            if err:
                return err
        for key in ('organization', 'industry'):
            err = optional_str(professional.get(key), field=f'professional.{key}')
            if err:
                return err

    accessibility = frontmatter.get('accessibility')
    if accessibility is not None:
        err = require_mapping(accessibility, field='accessibility')
        if err:
            return err
        assert isinstance(accessibility, dict)
        err = reject_unknown_keys(accessibility, _ACCESSIBILITY_KEYS, field='accessibility')
        if err:
            return err
        err = optional_str_list(
            accessibility.get('communication_needs'),
            field='accessibility.communication_needs',
        )
        if err:
            return err
    return None

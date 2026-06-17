#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["httpx>=0.27", "python-dotenv>=1.0"]
# ///

"""Configure and verify LazyMind providers from bootstrap environment."""

from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path
from typing import Any

import httpx
from dotenv import dotenv_values

ENV_FILE = Path.home() / "lazymind.bootstrap.env"
BASE_URLS = (
    "http://localhost:8090",
    "http://localhost:8000",
    "http://localhost:8080",
    "http://localhost:3000",
    "http://localhost:5000",
    "http://localhost:7000",
    "http://localhost:8081",
    "http://localhost:9000",
    "http://localhost:18080",
    "http://localhost:18081",
    "http://localhost:10000",
)
AUTH_PREFIXES = ("/api/authservice", "/authservice")
CORE_PREFIXES = ("/api/core", "/core", "")
MODEL_TYPE_ALIASES = {
    "evo_llm": {"llm"},
    "embed_main": {"embed"},
    "embed_image": {"cross_modal_embed"},
    "reranker": {"rerank"},
}
DEFAULT_KEY_ALIASES = {
    "llm": "llm",
    "evollm": "evo_llm",
    "vlm": "vlm",
    "text2image": "text2image",
    "embed": "embed_main",
    "embeddings": "embed_main",
    "embedmain": "embed_main",
    "embedimage": "embed_image",
    "imageembedding": "embed_image",
    "imaging": "embed_image",
    "reranker": "reranker",
    "rerank": "reranker",
    "tts": "tts",
    "stt": "stt",
}


class BootstrapError(RuntimeError):
    pass


def parse_args() -> None:
    argparse.ArgumentParser(
        description="Bootstrap LazyMind model/cloud providers and verify them.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Configuration sources:\n"
            "  1. ~/lazymind.bootstrap.env (highest priority)\n"
            "  2. Process environment variables\n\n"
            "Run:\n"
            "  uv run scripts/lazymind_bootstrap.py\n\n"
            "Common variables:\n"
            "  LAZYMIND_BOOTSTRAP_ADMIN_USERNAME=admin\n"
            "  LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD=admin\n"
            "  LAZYMIND_BOOTSTRAP_BASE_URL=http://localhost:8090\n"
            "  LAZYMIND_MODEL_PROVIDERS=SiliconFlow\n"
            "  LAZYMIND_CLOUD_SERVICES=Bocha,MinerU\n"
            "  LAZYMIND_DEFAULT_LLM=Qwen/Qwen3-32B\n"
            "  LAZYMIND_DEFAULT_EMBED_MAIN=Qwen/Qwen3-Embedding-8B\n"
            "  LAZYMIND_DEFAULT_EMBED_IMAGE=Qwen/Qwen3-VL-Embedding-8B\n"
            "  LAZYMIND_DEFAULT_VLM=Qwen/Qwen3.6-27B\n"
            "  LAZYMIND_DEFAULT_RERANKER=Qwen/Qwen3-Reranker-8B\n"
            "  LAZYMIND_DEFAULT_OCR=MinerU\n"
            "  LAZYMIND_DEFAULT_SEARCH=Bocha\n"
        ),
    ).parse_args()


def csv(value: str) -> list[str]:
    return [item.strip() for item in value.split(",") if item.strip()]


def token(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", value.strip().lower())


def squashed(value: str) -> str:
    return re.sub(r"\s+", "", value.strip().lower())


def slug(value: str) -> str:
    return re.sub(r"[^a-zA-Z0-9]+", "-", value.strip().lower()).strip("-") or "provider"


def load_config() -> dict[str, str]:
    config = dict(os.environ)
    file_config = {k: v for k, v in dotenv_values(ENV_FILE).items() if v is not None}
    config.update(file_config)
    source = f"loaded bootstrap env: {ENV_FILE}" if file_config else f"bootstrap env not found, using process environment only: {ENV_FILE}"
    print(source)
    return config


def required(config: dict[str, str], key: str) -> str:
    value = config.get(key, "").strip()
    if not value:
        raise BootstrapError(f"missing required config: {key}")
    return value


def keys_by_provider(config: dict[str, str], prefix: str) -> dict[str, str]:
    out: dict[str, str] = {}
    for key, value in config.items():
        upper = key.upper()
        if upper.startswith(prefix) and upper.endswith("_API_KEY"):
            out[token(upper[len(prefix) : -len("_API_KEY")])] = value
    return out


def candidate_bases(config: dict[str, str]) -> list[str]:
    raw = csv(config.get("LAZYMIND_BOOTSTRAP_BASE_URL", "")) or list(BASE_URLS)
    out: list[str] = []
    for value in raw:
        value = value.strip().rstrip("/")
        if value and "://" not in value:
            value = f"http://{value}"
        if value and value not in out:
            out.append(value)
    return out


def list_rows(data: Any, key: str) -> list[dict[str, Any]]:
    if isinstance(data, list):
        return [x for x in data if isinstance(x, dict)]
    if isinstance(data, dict):
        for candidate in (data.get(key), data.get("data")):
            if isinstance(candidate, list):
                return [x for x in candidate if isinstance(x, dict)]
    return []


def default_models(config: dict[str, str]) -> dict[str, str]:
    defaults: dict[str, str] = {}
    for key, value in config.items():
        upper = key.upper()
        if not upper.startswith("LAZYMIND_DEFAULT_") or upper in {"LAZYMIND_DEFAULT_OCR", "LAZYMIND_DEFAULT_SEARCH"}:
            continue
        raw_key = token(upper.removeprefix("LAZYMIND_DEFAULT_"))
        if value.strip():
            defaults[DEFAULT_KEY_ALIASES.get(raw_key, raw_key)] = value.strip()
    return defaults


class LazyMindClient:
    def __init__(self, config: dict[str, str]) -> None:
        timeout = float(config.get("LAZYMIND_BOOTSTRAP_TIMEOUT", "60"))
        self.config = config
        self.client = httpx.Client(timeout=timeout, headers={"Accept": "application/json", "User-Agent": "lazymind-bootstrap/4.0"})
        self.base = ""
        self.core_prefix = ""
        self.token = ""

    def close(self) -> None:
        self.client.close()

    def request(self, method: str, url: str, **kwargs: Any) -> Any:
        headers = kwargs.pop("headers", {})
        if self.token:
            headers = {**headers, "Authorization": f"Bearer {self.token}"}
        try:
            response = self.client.request(method, url, headers=headers or None, **kwargs)
            response.raise_for_status()
        except httpx.HTTPStatusError as exc:
            raise BootstrapError(f"HTTP {exc.response.status_code}: {exc.request.url}: {exc.response.text}") from exc
        except httpx.HTTPError as exc:
            raise BootstrapError(f"request failed: {url}: {exc}") from exc

        if not response.content:
            return None
        try:
            payload = response.json()
        except ValueError as exc:
            raise BootstrapError(f"invalid JSON from {response.url}: {response.text!r}") from exc
        if isinstance(payload, dict) and {"code", "message", "data"}.issubset(payload):
            if payload.get("code") not in {0, 200}:
                raise BootstrapError(f"api error {payload.get('code')}: {payload.get('message')}")
            return payload.get("data")
        return payload

    def core(self, method: str, path: str, **kwargs: Any) -> Any:
        return self.request(method, f"{self.base}{self.core_prefix}{path}", **kwargs)

    def login(self) -> None:
        username = self.config.get("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "admin")
        password = self.config.get("LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD", "admin")
        last_error: Exception | None = None
        for base in candidate_bases(self.config):
            for prefix in AUTH_PREFIXES:
                try:
                    data = self.request("POST", f"{base}{prefix}/auth/login", json={"username": username, "password": password})
                    self.token = data.get("access_token") if isinstance(data, dict) else ""
                    if not self.token:
                        raise BootstrapError(f"login response missing access_token: {data}")
                    self.base = base
                    print(f"login success: {username} via {base}")
                    return
                except BootstrapError as exc:
                    last_error = exc
        raise BootstrapError(f"auth login failed for all candidates: {last_error}")

    def discover_core(self) -> list[dict[str, Any]]:
        last_error: Exception | None = None
        for prefix in CORE_PREFIXES:
            try:
                providers = list_rows(self.request("GET", f"{self.base}{prefix}/model_providers", params={"category": "model"}), "providers")
                if providers:
                    self.core_prefix = prefix
                    print(f"core API detected: {self.base}{prefix}")
                    return providers
            except BootstrapError as exc:
                last_error = exc
        raise BootstrapError(f"core API discovery failed at {self.base}: {last_error}")

    def providers(self, category: str) -> list[dict[str, Any]]:
        return list_rows(self.core("GET", "/model_providers", params={"category": category}), "providers")

    def groups(self, provider_id: str) -> list[dict[str, Any]]:
        return list_rows(self.core("GET", f"/model_providers/{provider_id}/groups"), "groups")

    def models(self, provider_id: str, group_id: str) -> list[dict[str, Any]]:
        return list_rows(self.core("GET", f"/model_providers/{provider_id}/groups/{group_id}/models"), "models")


def find_provider(name: str, providers: list[dict[str, Any]]) -> dict[str, Any] | None:
    exact = [p for p in providers if squashed(str(p.get("name", ""))) == squashed(name)]
    if exact:
        return exact[0]
    loose = [p for p in providers if squashed(name) and squashed(name) in squashed(str(p.get("name", "")))]
    return loose[0] if len(loose) == 1 else None


def available_names(names: list[str], providers: list[dict[str, Any]]) -> list[str]:
    return [name for name in names if find_provider(name, providers)]


def ensure_groups(lm: LazyMindClient, providers: list[dict[str, Any]], names: list[str], api_keys: dict[str, str]) -> list[dict[str, Any]]:
    ready: list[dict[str, Any]] = []
    for requested in names:
        provider = find_provider(requested, providers)
        if not provider:
            print(f"skip unavailable provider {requested}")
            continue

        provider_id = str(provider.get("id") or "")
        provider_name = str(provider.get("name") or requested)
        group_name = f"bootstrap-{slug(provider_name)}"
        base_url = str(provider.get("base_url") or "").rstrip("/")
        api_key = api_keys.get(token(provider_name)) or api_keys.get(token(requested), "")
        group = next((g for g in lm.groups(provider_id) if str(g.get("name", "")) == group_name), None)
        group = group or (lm.groups(provider_id)[0] if lm.groups(provider_id) else None)
        payload = {"name": group_name if group is None else str(group.get("name") or group_name), "base_url": base_url, "api_key": api_key, "verify": True}

        if group:
            group_id = str(group.get("id") or "")
            if not group_id:
                raise BootstrapError(f"provider group has no id: {group}")
            if str(group.get("base_url") or "").rstrip("/") != base_url or (api_key and str(group.get("api_key") or "") != api_key):
                lm.core("PATCH", f"/model_providers/{provider_id}/groups/{group_id}", json=payload)
                print(f"updated group {payload['name']} for {provider_name}")
            else:
                print(f"reuse group {payload['name']} for {provider_name}")
        else:
            data = lm.core("POST", f"/model_providers/{provider_id}/groups", json=payload)
            group_id = str(data.get("id") if isinstance(data, dict) else "")
            if not group_id:
                raise BootstrapError(f"create group returned no id: {data}")
            print(f"created group {group_name} for {provider_name}")
        ready.append({"provider_id": provider_id, "group_id": group_id, "name": provider_name, "base_url": base_url, "api_key": api_key})
    return ready


def choose_model_id(groups: list[dict[str, Any]], model_key: str, wanted: str) -> str | None:
    accepted_types = {model_key, *MODEL_TYPE_ALIASES.get(model_key, set())}

    def matches(model: dict[str, Any], mode: str) -> bool:
        if str(model.get("model_type") or model.get("type") or "") not in accepted_types:
            return False
        name = str(model.get("name") or "")
        return name == wanted if mode == "exact" else name.casefold() == wanted.casefold() if mode == "casefold" else token(name) == token(wanted)

    for mode in ("exact", "casefold", "normalized"):
        for group in groups:
            for model in group.get("models", []):
                if matches(model, mode):
                    return str(model.get("id") or "")
    return None


def configure_models(lm: LazyMindClient, groups: list[dict[str, Any]]) -> None:
    groups_with_models = [{**g, "models": lm.models(g["provider_id"], g["group_id"])} for g in groups]
    selections = []
    for model_key, model_name in default_models(lm.config).items():
        model_id = choose_model_id(groups_with_models, model_key, model_name)
        if not model_id:
            raise BootstrapError(f"default model not found: {model_key} -> {model_name}")
        selections.append({"model_key": model_key, "model_id": model_id})
    if selections:
        lm.core("PUT", "/model_providers/selected_models", json={"selections": selections})
        print(f"set selected models: {[item['model_key'] for item in selections]}")
    else:
        print("no default models configured")


def configure_service_defaults(lm: LazyMindClient, groups: list[dict[str, Any]]) -> None:
    by_name = {squashed(g["name"]): g for g in groups}
    selections = []
    for category in ("ocr", "search"):
        wanted = lm.config.get(f"LAZYMIND_DEFAULT_{category.upper()}", "").strip()
        if wanted and (group := by_name.get(squashed(wanted))):
            selections.append({"category": category, "group_id": group["group_id"]})
        elif wanted:
            print(f"skip default {category}: provider not prepared: {wanted}")
    if selections:
        lm.core("PUT", "/model_providers/selected_providers", json={"selections": selections})
        print(f"set selected providers: {[item['category'] for item in selections]}")


def check_groups(lm: LazyMindClient, groups: list[dict[str, Any]]) -> None:
    for group in groups:
        data = lm.core(
            "POST",
            f"/model_providers/{group['provider_id']}/groups/{group['group_id']}:check",
            json={"provider_name": group["name"], "base_url": group["base_url"], "api_key": group["api_key"], "dry_run": True},
        )
        ok = bool(data.get("success", True)) if isinstance(data, dict) else True
        print(f"check {group['name']} ({group['group_id']}): {'PASS' if ok else 'FAIL'}")
        if not ok:
            raise BootstrapError(f"verification failed for {group['name']}: {data}")


def print_current_selection(lm: LazyMindClient) -> None:
    models = list_rows(lm.core("GET", "/model_providers/selected_models"), "selections")
    providers = list_rows(lm.core("GET", "/model_providers/selected_providers"), "selections")
def print_current_selection(lm: LazyMindClient) -> None:
    models = list_rows(lm.core("GET", "/model_providers/selected_models"), "selections")
    providers = list_rows(lm.core("GET", "/model_providers/selected_providers"), "selections")
    model_map = {item.get("model_key"): item.get("model_id") for item in models}
    provider_map = {item.get("category"): item.get("group_id") for item in providers}
    print(f"current selected models: {model_map}")
    print(f"current selected providers: {provider_map}")
def run() -> None:
    config = load_config()
    lm = LazyMindClient(config)
    try:
        lm.login()
        model_providers = lm.discover_core()
        model_names = csv(required(config, "LAZYMIND_MODEL_PROVIDERS"))
        cloud_names = csv(config.get("LAZYMIND_CLOUD_SERVICES", ""))
        model_keys = keys_by_provider(config, "LAZYMIND_MODEL_PROVIDER_")
        cloud_keys = keys_by_provider(config, "LAZYMIND_CLOUD_SERVICE_")

        model_groups = ensure_groups(lm, model_providers, model_names, model_keys)
        ocr_providers = lm.providers("ocr")
        search_providers = lm.providers("search")
        service_groups = ensure_groups(lm, ocr_providers, available_names(cloud_names, ocr_providers), cloud_keys)
        service_groups += ensure_groups(lm, search_providers, available_names(cloud_names, search_providers), cloud_keys)

        configure_models(lm, model_groups)
        configure_service_defaults(lm, service_groups)
        check_groups(lm, model_groups + service_groups)
        print_current_selection(lm)
    finally:
        lm.close()


def main() -> int:
    parse_args()
    try:
        run()
        return 0
    except Exception as exc:
        print(f"bootstrap failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

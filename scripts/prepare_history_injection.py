#!/usr/bin/env python3
"""Prepare Docker history-injection bundles only when PostgreSQL needs them."""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import stat
import sys
import tempfile
import time
import urllib.request
import zipfile
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import BinaryIO, Callable


MAX_EXTRACTED_BYTES = 2 << 30
MARKER_NAME = ".package-sha256"
PACKAGE_PREFIX = PurePosixPath("history-injection")


@dataclass(frozen=True)
class PackageConfig:
    url: str
    sha256: str
    size: int
    runtime_file_name: str
    conversation_ids: tuple[str, ...]


def env_enabled(value: str | None) -> bool:
    return (value or "").strip().lower() not in {"0", "false", "no", "off"}


def load_config(path: Path) -> PackageConfig:
    data = json.loads(path.read_text(encoding="utf-8"))
    url = str(data.get("url", "")).strip()
    digest = str(data.get("sha256", "")).strip().lower()
    size = data.get("size")
    runtime_file_name = str(data.get("runtimeFileName", "")).strip()
    conversation_ids = tuple(str(value).strip() for value in data.get("conversationIds", []))
    if not url.startswith(("http://", "https://")):
        raise ValueError("history injection URL must use HTTP(S)")
    if len(digest) != 64 or any(character not in "0123456789abcdef" for character in digest):
        raise ValueError("history injection SHA-256 is invalid")
    if not isinstance(size, int) or size <= 0:
        raise ValueError("history injection package size is invalid")
    if Path(runtime_file_name).name != runtime_file_name or runtime_file_name in {"", ".", ".."}:
        raise ValueError("history injection runtime file name is unsafe")
    if not conversation_ids or any(not value for value in conversation_ids):
        raise ValueError("history injection conversationIds must not be empty")
    if len(set(conversation_ids)) != len(conversation_ids):
        raise ValueError("history injection conversationIds contains duplicates")
    return PackageConfig(url, digest, size, runtime_file_name, conversation_ids)


def database_has_all_conversations(dsn: str, conversation_ids: tuple[str, ...]) -> bool:
    import psycopg

    try:
        with psycopg.connect(dsn, connect_timeout=10) as connection:
            with connection.cursor() as cursor:
                cursor.execute(
                    "SELECT COUNT(DISTINCT id) FROM conversations WHERE id::text = ANY(%s)",
                    (list(conversation_ids),),
                )
                count = int(cursor.fetchone()[0])
    except psycopg.errors.UndefinedTable:
        print("History injection: conversations table does not exist yet; package preparation is required")
        return False
    return count == len(conversation_ids)


def file_identity(path: Path) -> tuple[str, int]:
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            digest.update(chunk)
            size += len(chunk)
    return digest.hexdigest(), size


def archive_is_valid(path: Path, config: PackageConfig) -> bool:
    if not path.is_file():
        return False
    digest, size = file_identity(path)
    return digest == config.sha256 and size == config.size


def default_open_url(url: str, timeout: int) -> BinaryIO:
    request = urllib.request.Request(url, headers={"User-Agent": "LazyMind-history-injection/1"})
    return urllib.request.urlopen(request, timeout=timeout)


def download_archive(
    config: PackageConfig,
    destination: Path,
    *,
    attempts: int = 3,
    open_url: Callable[[str, int], BinaryIO] = default_open_url,
) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    last_error: Exception | None = None
    for attempt in range(1, attempts + 1):
        temporary = destination.with_name(f".{destination.name}.download-{os.getpid()}")
        try:
            digest = hashlib.sha256()
            size = 0
            with open_url(config.url, 120) as response, temporary.open("wb") as output:
                while chunk := response.read(1024 * 1024):
                    output.write(chunk)
                    digest.update(chunk)
                    size += len(chunk)
            if size != config.size:
                raise ValueError(f"package size mismatch: got {size}, want {config.size}")
            actual_digest = digest.hexdigest()
            if actual_digest != config.sha256:
                raise ValueError(f"package SHA-256 mismatch: got {actual_digest}, want {config.sha256}")
            os.replace(temporary, destination)
            return
        except Exception as error:
            last_error = error
            temporary.unlink(missing_ok=True)
            if attempt < attempts:
                time.sleep(attempt)
    raise RuntimeError(f"download history injection package: {last_error}") from last_error


def prepared_output_is_valid(output_root: Path, digest: str) -> bool:
    marker = output_root / MARKER_NAME
    if not marker.is_file():
        return False
    if marker.read_text(encoding="utf-8").strip().lower() != digest:
        return False
    return any(path.is_file() for path in output_root.rglob("*.zip"))


def _safe_archive_relative_path(name: str) -> Path | None:
    if "\\" in name:
        raise ValueError(f"history injection package contains unsafe path {name!r}")
    value = PurePosixPath(name)
    if value.is_absolute() or ".." in value.parts:
        raise ValueError(f"history injection package contains unsafe path {name!r}")
    if not value.parts or value.parts[0] != PACKAGE_PREFIX.name:
        return None
    relative = value.relative_to(PACKAGE_PREFIX)
    if not relative.parts:
        return Path(".")
    return Path(*relative.parts)


def extract_archive(archive: Path, output_root: Path, digest: str) -> None:
    output_root.parent.mkdir(parents=True, exist_ok=True)
    staging_parent = Path(tempfile.mkdtemp(prefix=".history-injection-staging-", dir=output_root.parent))
    staging_root = staging_parent / "bundles"
    staging_root.mkdir()
    bundle_count = 0
    extracted_bytes = 0
    try:
        with zipfile.ZipFile(archive) as package:
            for entry in package.infolist():
                relative = _safe_archive_relative_path(entry.filename)
                if relative is None or relative == Path("."):
                    continue
                mode = entry.external_attr >> 16
                if stat.S_ISLNK(mode):
                    raise ValueError(f"history injection package contains symlink {entry.filename!r}")
                extracted_bytes += entry.file_size
                if extracted_bytes > MAX_EXTRACTED_BYTES:
                    raise ValueError("history injection package exceeds the extracted size limit")
                target = staging_root / relative
                if entry.is_dir():
                    target.mkdir(parents=True, exist_ok=True)
                    continue
                file_type = stat.S_IFMT(mode)
                if file_type and file_type != stat.S_IFREG:
                    raise ValueError(f"history injection package contains unsupported file {entry.filename!r}")
                target.parent.mkdir(parents=True, exist_ok=True)
                with package.open(entry) as source, target.open("wb") as destination:
                    shutil.copyfileobj(source, destination, 1024 * 1024)
                if target.suffix.lower() == ".zip":
                    bundle_count += 1
        if bundle_count == 0:
            raise ValueError("history injection package contains no bundle ZIP files")
        (staging_root / MARKER_NAME).write_text(f"{digest}\n", encoding="utf-8")
        backup_root = output_root.with_name(f".{output_root.name}.backup-{os.getpid()}")
        if backup_root.exists():
            shutil.rmtree(backup_root)
        if output_root.exists():
            os.replace(output_root, backup_root)
        try:
            os.replace(staging_root, output_root)
        except Exception:
            if backup_root.exists() and not output_root.exists():
                os.replace(backup_root, output_root)
            raise
        shutil.rmtree(backup_root, ignore_errors=True)
    finally:
        shutil.rmtree(staging_parent, ignore_errors=True)


def main() -> int:
    if not env_enabled(os.environ.get("LAZYMIND_HISTORY_INJECTION_ENABLED")):
        print("History injection: disabled")
        return 0

    config_path = Path(os.environ.get(
        "LAZYMIND_HISTORY_INJECTION_CONFIG",
        "/opt/lazymind-bootstrap/history-injection-package.json",
    ))
    cache_root = Path(os.environ.get(
        "LAZYMIND_HISTORY_INJECTION_CACHE_ROOT",
        "/var/lib/lazymind/uploads/.history-injection",
    ))
    output_root = Path(os.environ.get(
        "LAZYMIND_HISTORY_INJECTION_ROOT",
        str(cache_root / "bundles"),
    ))
    dsn = os.environ.get("ACL_DB_DSN", "").strip()
    if not dsn:
        raise ValueError("ACL_DB_DSN is required for Docker history injection preparation")

    config = load_config(config_path)
    if database_has_all_conversations(dsn, config.conversation_ids):
        print("History injection: all configured conversations already exist; download skipped")
        return 0
    if prepared_output_is_valid(output_root, config.sha256):
        print("History injection: verified extracted cache is ready; download skipped")
        return 0

    archive = cache_root / "cache" / config.runtime_file_name
    if archive_is_valid(archive, config):
        print("History injection: reusing verified package cache")
    else:
        print(f"History injection: downloading {config.url}")
        download_archive(config, archive)
    extract_archive(archive, output_root, config.sha256)
    print(f"History injection: package prepared at {output_root}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"History injection preparation failed: {error}", file=sys.stderr)
        raise SystemExit(1)

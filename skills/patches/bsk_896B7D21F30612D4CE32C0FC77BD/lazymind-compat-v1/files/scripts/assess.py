#!/usr/bin/env python3
"""Stage one explicitly owned Skill and report evidence without installing it."""
import argparse
import hashlib
import io
import json
import re
import stat
import sys
import tempfile
import zipfile
from pathlib import Path, PurePosixPath
from urllib.parse import unquote, urlencode, urlsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler

MAX_ARCHIVE = 20 * 1024 * 1024
MAX_EXPANDED = 50 * 1024 * 1024
ALLOWED_HOSTS = {
    "clawhub.ai",
    "api.skillhub.cn",
    # SkillHub's download API redirects published archives to this fixed COS host.
    "skillhub-1388575217.cos.accelerate.myqcloud.com",
}


class RestrictedRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        parsed = urlsplit(newurl)
        if parsed.scheme != "https" or parsed.hostname not in ALLOWED_HOSTS or parsed.username or parsed.port:
            raise ValueError("Download redirect left the supported registry hosts")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def source_urls(value):
    parsed = urlsplit(value)
    if parsed.scheme != "https" or parsed.username or parsed.password or parsed.port or parsed.query or parsed.fragment:
        raise ValueError("Use a complete HTTPS Skill page URL without query, credentials or fragment")
    parts = [unquote(p) for p in parsed.path.strip("/").split("/")]
    if any(not re.fullmatch(r"[A-Za-z0-9_-]+", p) for p in parts):
        raise ValueError("Invalid Skill page path")
    if parsed.hostname in ("skillhub.cn", "www.skillhub.cn") and len(parts) == 3 and parts[0] == "skills":
        owner, slug = parts[1:]
        return slug, "https://api.skillhub.cn/api/v1/download?" + urlencode({"slug": f"@{owner}/{slug}"}), None, owner
    if parsed.hostname == "clawhub.ai" and (len(parts) == 2 or (len(parts) == 3 and parts[1] == "skills")):
        owner, slug = parts[0], parts[-1]
        query = urlencode({"owner": owner})
        return slug, "https://clawhub.ai/api/v1/download?" + urlencode({"slug": slug, "owner": owner}), f"https://clawhub.ai/api/v1/skills/{slug}?{query}", owner
    raise ValueError("Expected /skills/<namespace>/<slug> on SkillHub or /<owner>/skills/<slug> on ClawHub; bare slugs are ambiguous")


def fetch(url):
    opener = build_opener(RestrictedRedirect())
    with opener.open(Request(url, headers={"User-Agent": "LazyMind-skill-guard/1.1"}), timeout=45) as response:
        body = response.read(MAX_ARCHIVE + 1)
        if len(body) > MAX_ARCHIVE:
            raise ValueError("Download exceeds 20 MiB limit")
        return body


def extract_archive(body, dest):
    """Validate the entire archive before writing any entry. Never execute content."""
    with zipfile.ZipFile(io.BytesIO(body)) as archive:
        entries = archive.infolist()
        if len(entries) > 1000 or sum(e.file_size for e in entries) > MAX_EXPANDED:
            raise ValueError("Archive exceeds extraction limits")
        seen = set()
        for entry in entries:
            name = entry.filename
            path = PurePosixPath(name)
            mode = entry.external_attr >> 16
            if (not name or "\\" in name or "\x00" in name or path.is_absolute()
                    or ".." in path.parts or ":" in name or stat.S_ISLNK(mode)
                    or entry.flag_bits & 1 or name.rstrip("/").casefold() in seen):
                raise ValueError("Unsafe or duplicate archive entry")
            seen.add(name.rstrip("/").casefold())
        archive.extractall(dest)
    roots = list(dest.rglob("SKILL.md"))
    if len(roots) != 1:
        raise ValueError("Expected exactly one SKILL.md in the downloaded package")
    return roots[0].parent


# Heuristics are review leads, never a full malware or prompt-injection verdict.
RULES = [
    ("instruction_override", r"ignore\s+(all\s+)?previous\s+instructions|忽略.{0,8}(之前|此前|系统).{0,8}指令", "high", "Potential instruction override; inspect context"),
    ("download_execute", r"curl[^\n|]*\|\s*(?:ba)?sh|wget[^\n|]*\|\s*(?:ba)?sh", "high", "Downloaded content is piped to a shell"),
    ("dynamic_execution", r"\b(?:eval|exec)\s*\(|shell\s*=\s*True", "high", "Dynamic execution requires manual review"),
    ("credential_access", r"\.ssh[/\\]|\.aws[/\\]|id_rsa|(?:os\.)?environ\[", "medium", "Sensitive path or environment access"),
    ("credential_literal", r"(?:api_key|token|password)\s*=\s*['\"][A-Za-z0-9_+/=-]{16,}['\"]", "high", "Possible embedded credential; value redacted"),
    ("network_request", r"requests\.(?:get|post|put)|urlopen\s*\(|fetch\s*\(", "info", "Network capability; review destinations and transmitted data"),
    ("file_read", r"\bopen\s*\(|\.read_text\s*\(|\.read_bytes\s*\(", "info", "File-reading capability; review user control of paths"),
]


def local_scan(root):
    files, findings = [], []
    for file in sorted(root.rglob("*")):
        if not file.is_file():
            continue
        body = file.read_bytes()
        item = {"path": file.relative_to(root).as_posix(), "bytes": len(body), "sha256": hashlib.sha256(body).hexdigest()}
        try:
            content = body.decode("utf-8")
            if "\x00" in content:
                raise UnicodeError("binary")
            item["reviewed_as_text"] = True
            for number, line in enumerate(content.splitlines(), 1):
                for code, pattern, severity, explanation in RULES:
                    if re.search(pattern, line, re.I):
                        findings.append({"file": item["path"], "line": number, "type": code, "severity": severity, "description": explanation})
        except UnicodeError:
            item["reviewed_as_text"] = False
            findings.append({"file": item["path"], "type": "unreviewed_binary", "severity": "medium", "description": "Binary/non-UTF8 content requires separate review"})
        files.append(item)
    return files, findings


def assess(source, output):
    slug, download, metadata_url, owner = source_urls(source)
    moderation = None
    version = None
    if metadata_url:
        meta = json.loads(fetch(metadata_url))
        if meta.get("owner", {}).get("handle", "").casefold() != owner.casefold():
            raise ValueError("Registry owner does not match requested publisher")
        version = (meta.get("latestVersion") or {}).get("version")
        moderation = meta.get("moderation")
        if isinstance(moderation, dict) and moderation.get("isMalwareBlocked"):
            raise ValueError("Registry blocks this package as malicious")
        if version:
            download += "&" + urlencode({"version": version})
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix=slug + "-", dir=output))
    body = fetch(download)
    root = extract_archive(body, stage / "package")
    files, findings = local_scan(root)
    report = {
        "schema_version": 1, "source_url": source, "resolved_url": download,
        "publisher": owner, "version": version, "archive_sha256": hashlib.sha256(body).hexdigest(),
        "package_path": str(root), "mode": "assess-only", "installed": False,
        "download_status": "success", "local_scan_status": "success",
        "remote_scan_status": "not_tested", "remote_scan_reason": "Snyk semantic scan is separate and requires SNYK_TOKEN; local checks do not replace it",
        "risk_verdict": "manual_review_required", "registry_moderation": moderation,
        "files": files, "findings": findings,
        "limitations": ["Heuristic matches may be false positives", "No downloaded code executed", "No absence-of-threat guarantee", "No automatic installation"],
    }
    report_path = stage / "assessment.json"
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    return {**report, "report_path": str(report_path)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", help="SkillHub or ClawHub page URL with publisher")
    parser.add_argument("--assess-only", action="store_true", help="Default behavior: never installs")
    parser.add_argument("--output", default="skill-guard-reports", help="Report/staging directory in the current workspace")
    args = parser.parse_args()
    try:
        report = assess(args.source, args.output)
    except Exception as exc:
        # Do not print raw remote response bodies, URLs with credentials or traceback.
        print(json.dumps({"status": "blocked", "error_type": type(exc).__name__, "message": "Package assessment failed; verify the source URL, archive format, registry access and limits", "installed": False}))
        return 1
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())

# Portable marketing Skill supplement

This extends the existing experiment in this directory. It does not create a
second Example, fix Claude.ai's installer, or change LazyMind Core. The original
execution evidence remains about the unmodified upstream marketing Skill.

## Two different Skills with the same name

Both files exist at commit `c75fda00ff0e41c8ee331ae312c8a07f1967653b`:

| Source path | Git blob | Scope | PR #873 execution evidence |
| --- | --- | --- | --- |
| `marketing/skills/competitive-brief/SKILL.md` | `1a7df218996ed350c67e6899a60321f205bb69d6` | Positioning, messaging and content gaps; seven-section report | Yes, with the failures recorded in EVALUATION.md |
| `product-management/skills/competitive-brief/SKILL.md` | `05ca7ec4314ec1a73a882562a364726870944cff` | Product/feature comparison and product-strategy implications | None in this experiment |

The PM-oriented business task does not change the source Skill's identity. The
marketing dependency is `marketing/CONNECTORS.md`, blob
`f4d375273c56cfeb923022556ac5ec60e4c818ff`. The product-management dependency is a
different file, blob `aa15b5ccfb4cd0a5d8ea668632634c25061bb40b`. Exact paths, sizes,
SHA-256 digests and source links are recoverable from [source-lock.json](source-lock.json).
Match repository + commit + path + blob, never the frontmatter name alone.

## Dependency failure and local repair

The pinned marketing directory contains only SKILL.md. Its `../../CONNECTORS.md`
link works inside the complete upstream plugin but escapes a standalone Skill
directory. The recorded LazyMind import contains SKILL.md and a platform-added
run.py, not CONNECTORS.md. Preflight did not flag the missing parent reference.
This is consistent with upstream [Issue #1381](https://github.com/anthropics/knowledge-work-plugins/issues/1381),
but this supplement neither modifies nor tests Claude.ai's installer.

The builder emits a local folder and deterministic ZIP containing:

```text
SKILL.md
references/CONNECTORS.md
LICENSE
NOTICE.txt
ORIGIN.json
```

The only Skill changes are a `competitive-brief-marketing` name/slash-trigger
alias, a local CONNECTORS link and a modification notice. The research
instructions are otherwise byte-equivalent after reversing those changes.
CONNECTORS and LICENSE are copied exactly. The upstream LICENSE has nonstandard
trailing text, which is retained rather than silently normalized. The pinned
upstream tree contains no NOTICE file. Source bodies and generated ZIPs are not
committed in this PR; attribution and exact hashes accompany generated packages.

CONNECTORS is a category catalog, not tool configuration. Its reference to an
upstream `.mcp.json` does not imply this package includes one. No server, API key,
subscription, permission or web-search capability is provisioned. The original
offline task and fact cards remain separate explicit inputs.

## Build and check without a model

Use Python 3.10+ and a fresh output path. With a checkout of the pinned upstream
commit (or the exact three files in their original paths):

```sh
python3 examples/competitive-brief-skill2workflow/scripts/package_skill.py \
  --source-root /path/to/pinned/knowledge-work-plugins \
  --out /tmp/competitive-brief-package-new
```

Alternatively replace `--source-root ...` with `--fetch`. That option performs
three bounded public reads, verifies all hashes and stops on any HTTP error; it
does not search for credentials or retry around access/rate limits. The offline
mode is suitable for public files obtained through an authorized connector.

The ZIP has SKILL.md at its root. The existing Core source contract accepts
`skill.zip_base64` with `skill.zip_sha256` instead of `skill.url`; see
`backend/core/workflow/external_agent_skill.go` at audited commit
`71a0e5a829f1ef8d9a5d9e9ce720386ba3c69d0e`. This is implementation evidence, not
proof that the newly packaged variant has been installed or executed. Inspect
the generated folder before an authorized test import; do not replace the
original experiment's provenance with this adapted source.

For the focused tests, the source checkout must also contain the pinned
product-management SKILL.md used as a deliberate wrong-source input:

```sh
python3 examples/competitive-brief-skill2workflow/scripts/test_supplement.py \
  --source-root /path/to/pinned/knowledge-work-plugins -v
python3 examples/competitive-brief-skill2workflow/scripts/check_recovery.py
```

Fifteen tests cover the missing dependency, standalone link resolution,
same-name source rejection, altered connector rejection, path escape, exact
limited transformations, deterministic ZIP output and preservation of recorded
failure/recovery semantics. The recovery tests replay the original real API
records and mutate copies to ensure misleading success claims are rejected.
They do not simulate a successful live product run. The original verifier also
calls the recovery consistency checks.

## Evidence reduction and remaining acceptance

[retention.json](evidence/retention.json) lists 19 removed files and their hashes:
repeated polls/projections, repeated response bodies, one redundant screenshot,
an intermediate artifact and the full edit request containing a duplicate report.
Their 326,092 bytes remain available at the pinned original commit. Terminal
task failure, failed/completed session projections, unchanged graph, actual retry
request/command, reused input revision witnesses, failed edit responses, final
report, baseline outputs and the recovered screenshot remain in the working
tree. No failure was removed to improve the outcome.

The local package resolves its bundled dependency and reproduces byte-for-byte.
The existing recordings confirm same-session assisted recovery with reused
inputs, not autonomous E2E success. Execution failure, groundedness problems,
503/500 edit failures and lack of mandatory/human approval remain unchanged.
The package has not had a new hosted import/conversion run. Keep the PR Draft
until its intended acceptance scope and these remaining gaps have been reviewed;
do not advertise a successful autonomous business Showcase.

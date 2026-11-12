# GitHub skill import implementation plan

**Goal:** Accept common GitHub skill links and import their actual directory reliably.

**Architecture:** Keep URL resolution in `skillv2/sourceurl`, retrieval in `skillv2/service`, and archive validation in `skillv2/skillpackage`. The HTTP downloader gains an optional source-aware method; existing downloader implementations remain compatible. GitHub repository imports discover a unique `SKILL.md`; multiple skills require a specific directory or document link.

**Constraints:** Download limit 100 MiB; existing 32 MiB per-file, 128 MiB expanded-package and 512-file bounds remain. No new dependencies. Do not weaken archive path, duplicate-path or symlink validation. Preserve original source URLs and do not auto-send the Chat draft. The user explicitly delegated design decisions and waived further questions.

- [x] Extend GitHub repository/tree/blob/raw document parsing, query/fragment handling and locked archive resolution. Cover slash refs and unsafe inputs with resolver tests.
- [x] Add a bounded metadata cache with concurrent request coalescing, trusted-host optional token authentication and cancellable short transient retries. Follow [GitHub Trees API](https://docs.github.com/en/rest/git/trees) truncation rules and [rate-limit guidance](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).
- [x] Add `HTTPZipDownloader.DownloadSource(context.Context, SourceInput) (DownloadedZip, error)`. Use Git Trees metadata and pinned raw downloads for target files; fall back to archive download only on retrieval failures, never size, ambiguity or safety failures.
- [x] Add `DownloadedZip.PathPrefix *string` and `PackageRoot string`. Source-aware results can override archive selection and retain the selected directory's metadata fallback name.
- [x] Add `skillpackage.ReadGitHubZip(zipPath, prefix)`: explicit directory selection or unique repository skill discovery, counting limits after selection. Test with unrelated files, multiple skills, mixed-case documents and unsafe archives.
- [x] Add typed HTTP semantic errors and HTTP 413 mapping for import size failures; expose `github_multiple_skills` for actionable recovery.
- [x] Retain the import dialog during requests, show actionable size/multiple-skill errors, provide Chat handoff containing the original URL, remove admin URL rewriting, and allow long-running URL requests. Cover error categorization and UI handoff with frontend tests.
- [x] Run all skillv2 Go tests, dependent bundle/workflow tests, targeted frontend tests and relevant type/build checks. Review the combined diff and fix concrete findings before completion.

For every behavior, add a failing regression first, implement, then rerun the focused test. Baseline `go test ./skillv2/sourceurl ./skillv2/service ./skillv2/handler` passed before implementation (existing macOS Keychain deprecation warnings only).

## Operations

`LAZYMIND_GITHUB_TOKEN` optionally authenticates REST metadata requests; `GITHUB_TOKEN` is the fallback environment variable. Tokens are sent only to HTTPS `api.github.com`, and are removed on redirects to other hosts. Public imports still work anonymously. Successful metadata is cached for 30 seconds with a 64-entry / 16 MiB bound and concurrent requests are coalesced. Short transient retries are bounded; interactive API rate exhaustion can validate archive refs using unauthenticated HEAD requests and continue through the archive compatibility path. Build/lock consumers keep strict pinned resolution through `ResolveGitHubPageURL`; handler/workflow imports opt into `ResolveGitHubImportURL`.

The 100 MiB bound covers downloaded bytes, including partial directory attempts before archive fallback. The existing expanded-package/file-count limits remain independent. Repository home links with multiple skills return `github_multiple_skills`; a specific directory or `SKILL.md` link imports that selected skill. Explicit root `SKILL.md` links include nested resources even when those resources contain another example `SKILL.md`.

## Verification

- `go test ./skillv2/... ./cmd/builtin-skill-bundle`: passed.
- `go test ./workflow -run 'Skill|ExternalAgentWorkflow|ExternalSkill' -count=1`: passed.
- Source URL and downloader race tests: passed; `go vet` of sourceurl/service/skillpackage/httperr: passed.
- Six targeted frontend suites: 102 tests passed; production `pnpm build`: passed.
- Full frontend TypeScript diagnostics: 197 on clean HEAD and 197 on this change, with identical normalized diagnostics.
- The full workflow package has two pre-existing model configuration failures (`TestGenerationLoadsCurrentOwnerSelectionAtExecution`, `TestExternalCapabilitiesMatchFrontendSystemModelSelection`), reproduced from a clean archived HEAD. Skill-related workflow tests pass.
- Independent review covered both personal and admin import paths, explicit root selection, archive fallback, limit categorization, authentication, cache isolation, cancellation, and preservation of pinned builtin lock resolution. All important findings were fixed.

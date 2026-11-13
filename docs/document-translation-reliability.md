# Document translation reliability

The keyless web providers share a single in-flight request slot per provider in
each core process. Their existing pacing also shares this provider-wide scope,
instead of allocating independent quotas to each user. HTTP 429 imposes a
shared cooldown of at least 30 seconds; Retry-After is respected when longer.
This is process-local protection, not distributed coordination across replicas.

Document blocks use five attempts with exponential backoff (2/4/8/16 seconds),
extended by the provider cooldown. Successful blocks are atomically checkpointed
next to the job source on persistent upload storage. Keys include the user,
provider snapshot, language and input. Automatic retries of the same job reuse
these checkpoints, including after a core restart. A separately created new job
has a separate source path and does not share these checkpoints. Empty responses
are errors; canceled work cannot be assembled as a successful partial result.

References reviewed on 2026-11-10:

- https://github.com/PDFMathTranslate/PDFMathTranslate-next/blob/main/pdf2zh_next/translator/base_translator.py
- https://github.com/PDFMathTranslate/PDFMathTranslate-next/blob/main/pdf2zh_next/translator/cache.py
- https://github.com/PDFMathTranslate/PDFMathTranslate-next/blob/main/pdf2zh_next/translator/rate_limiter/qps_rate_limiter.py
- https://github.com/PDFMathTranslate/PDFMathTranslate-next/blob/main/pdf2zh_next/translator/translator_impl/google.py

The design adopts persistent successful-result reuse and paced requests. It
does not copy the Google adapter's 5000-character input truncation or error text
returned as a translation. PDF layout extraction and semantic completeness still
need separate evaluation; rendering all extracted blocks does not prove every
source paragraph was extracted. Checkpoints currently follow source artifact
storage retention and require cleanup together with those artifacts.

## PDF rendering and diagnostics

The renderer keeps source pages, images, vector artwork and protected mathematical
content. It first tries the existing text region, available blank space, tighter
line spacing and smaller type. Text that still does not fit continues at its
position in the document, moving following content forward. There is no plain-text
whole-document fallback, end-of-document supplement, or overflow navigation link.
Continuation pages can make the translation longer than its source.
New extraction separates prose runs around protected math spans. Older saved
layouts need a fresh translation to adopt this segmentation; rerendering old
translated blocks cannot reconstruct missing formula boundaries.

Quality checks are advisory for both initial generation and draft revision:
block counts, text extraction, images, vectors and continuation diagnostics are
recorded without rejecting an otherwise generated PDF. Diagnostics are saved next
to the output as `.diagnostics.json`; a failure to write that file does not prevent
publication. Transport, authorization, malformed input and output-write failures
remain real failures. Retiring an artifact also retires its diagnostics and source
checkpoint directory, except where a current artifact still shares those paths.

The translated PDF has a selectable text layer. The optional synchronized-scroll
button links relative scroll progress in either direction, not semantic paragraph
positions. Progress polling only updates the notice during an active job; terminal
transitions update the selected artifact. One poll is in flight at a time, and late
responses after cancellation or unmount cannot overwrite the active page.

## Real-paper verification (2026-11-10)

Five papers were generated using DeepL Web on deployed revision `86a4ec611`.
Each request was a cache miss, with one background-job attempt and no whole-job
retry or manual cache copying. All resulting pages could be rendered; browser
PDF downloads matched the generated files by SHA-256.

| Paper | Source pages | Translation pages |
| --- | ---: | ---: |
| Attention Is All You Need | 15 | 15 |
| Rapid and Sensitive Chemical Analysis of Individual Picolitre Droplets by Mass Spectrometry | 8 | 9 |
| AlphaFold | 12 | 16 |
| Best Practices for Scientific Computing | 7 | 7 |
| Teleportation of Quantum States | 5 | 5 |

These 52 pages establish first-attempt generation and readability of the output
files, not translation accuracy or complete semantic layout preservation. That
revision still showed partially translated formula/code content and sparse
continuation pages. Later review fixes have synthetic regression coverage; the
historical five-paper run must not be represented as a new full-paper visual
acceptance of those later changes.

Earlier experiments used a whole-document text-only fallback. Their page counts
and text-completeness checks are obsolete for the current renderer and do not
establish preservation of figures, formulas or columns.

CI runs the office-convert-service regression suite with its pinned PyMuPDF
version and CJK fonts, in addition to the Go and frontend test suites.

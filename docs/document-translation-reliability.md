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

When original text boxes cannot hold all translations, the renderer emits a
complete reflowed translation, with source page labels. This deliberately trades
original layout and embedded figures for readable, complete translated text;
the original PDF remains available in the adjacent reader. Mathematical Unicode
is compatibility-normalized and a secondary font handles missing symbols.

## Real-paper verification (2026-11-10)

| Paper | Web provider | Source characters sent through job | Translated blocks | Output pages | Missing printable translations |
| --- | --- | ---: | ---: | ---: | ---: |
| DeepSeek-V4.1-Flash | DeepL Web | 157613 | 980 | 71 | 0 |
| Composed Image Retrieval using Contrastive Learning and Task-oriented CLIP-based Features | Google Web | 86781 | 287 | 31 | 0 |
| Attention Is All You Need | DeepL Web | 38607 | 464 | 25 | 0 |

All three used real web translation requests and completed the backend job.
Saved results were re-rendered during the renderer fixes without repeating web
requests. All three ultimately used the complete reflow fallback. Verification
compares normalized printable translation text against the PDF's extracted text,
not just block counts. It does not assert translation accuracy or that the source
layout extractor found every possible text element. Original figures remain in
the source reader, not in the reflow fallback.

The long-paper trial initially exposed an actual 348/491 rendering failure.
The retrieval-paper trial exposed mismatched text despite successful block
counts. Both now pass text completeness verification. One deployment from a
concurrent task interrupted the long-paper retry; the test job was explicitly
requeued after restoring the intended core image. Automatic checkpoint reuse
then completed without retranslating the full paper.

Visual review found that broad first-page metadata detection discarded the
Attention paper's title. Extraction now retains short titles, edge prose and
text overlapping figures instead of using these broad exclusion rules. All
three jobs were rerun after this change; the table reports the final results.
The final runs also exercised service interruption and checkpoint recovery.

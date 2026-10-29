# PDF formula reading

The original-PDF reader can parse formulas with the selected official MinerU service and show a LaTeX preview on hover. The formula marker also supports click and keyboard activation. The card exposes the recognized source for copying and shows that source even when the preview renderer does not support a command.

## Enable and use

Configure and select an official MinerU OCR credential in external-service settings. Open an original PDF in a knowledge document or paper attachment and enable formula parsing. Browser fingerprint validation requires HTTPS or localhost; insecure remote HTTP displays an explicit unsupported-context message. Parsing runs as a durable background job; the reader polls its status. Once ready, hover over an equation or activate its formula marker. Dragging to select document text keeps the existing text-selection behavior. Escape dismisses the formula card.

Parsing requires document write permission; existing results require read permission. The initial read does not upload the document. Local or custom MinerU services are currently reported as unavailable for this feature and are never silently redirected to the official service. Only the original PDF is supported; translated or regenerated PDFs must not reuse original coordinates.

## Independent document structure

PDF IR does not call the retrieval parser, create text chunks, or update vector indexes. It does not depend on the document's retrieval processing status. The original PDF is snapshotted for upload without modifying the stored source. Its SHA-256 fingerprint binds the result to those exact bytes; the browser checks the displayed PDF against the same fingerprint before enabling formula hit regions.

The backend retains the raw MinerU ZIP and a versioned IR JSON under the document's artifact directory. These are durable files next to the uploaded document, so that directory must be on persistent storage. Internal filesystem paths and credentials are not part of the reader response. Job payloads contain document identifiers, source fingerprints and parser revision, not credentials. Jobs resolve the selected credential at execution time.

The IR preserves returned block types, normalizes equation blocks to `formula`, and includes LaTeX, source text, page and geometry. The raw ZIP remains available server-side for information outside the current reader projection, including tables and images. This is not a complete semantic graph of the paper.

Coordinates use `normalized_top_left`: `[left, top, right, bottom]` in `[0,1]`, relative to the original visible page with its intrinsic rotation. Page indices start at zero. Invalid or missing boxes are omitted instead of guessed. Native text selection does not provide formula identity; pointer hit testing uses the independent formula regions. Browser zoom and page scaling do not change stored geometry.

## API

`GET /api/core/datasets/{dataset}/documents/{document}/pdf-ir` returns the current result without starting a job. `POST` to the same path starts parsing; `{ "force": true }` requests a new parse after a completed result. An active job is reused. Responses use the `data` envelope and statuses `not_requested`, `queued`, `running`, `ready`, `failed`, or `unavailable`. A ready response includes `document`, with `version`, `source_sha256`, `parser`, `coordinate_space`, `pages`, `blocks`, and optional `warnings`.

## Limits and correctness

The first version targets detected standalone equations. It does not promise inline formula detection, symbol-level selection, variable explanations, or automatic formula-reference links. Recognition and rendering are separate: valid LaTeX can still contain an OCR error. Users should compare critical equations with the original PDF. Missing geometry prevents hover rather than placing a misleading hit region. A mismatched source fingerprint prevents reuse and requires parsing the displayed original again.

The official parser currently uses the `vlm` model. Source PDFs are limited to 128 MiB and result archives to 256 MiB, with bounded JSON decoding. Restarted or retried work may incur another service request; the remote provider does not share the application's job transaction.

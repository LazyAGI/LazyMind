# Use the LazyMind plugin to review and edit a PPT from your Agent

[中文](README.CN.md) · [Examples](../README.md)

**Status: original prompt and historical output collected; current-version reproduction pending.** This reuses the existing Palace Museum case. The DSH recording and matching PDF are the primary visual reference because the architecture and artifact imagery are clearer; the Codex recording is supplementary. See the [material inventory](sources.md).

![Four-page DSH Palace Museum reference](reference/preview.jpg)

[Download the historical PDF (lossless ZIP)](reference/palace-museum-dsh-reference.zip). This is an existing output, not a new run. Its matching PPTX has not yet been supplied.

## What this case should demonstrate

Keep using Codex or DSH to start the task. LazyMind provides inspectable intermediate material and a Workflow panel for review and editing, so the user can adjust the work before delivery. The value to verify is the connected process—not simply that an Agent can generate slides.

The public references are [Codex + Palace Museum PPT](https://www.bilibili.com/video/BV1TppR6xEj7/) and [DSH + LazyMind plugin](https://www.bilibili.com/video/BV1Xcpt6REjc/). The owner-supplied recordings have been inspected: they show source/outline/per-slide prompt review, local editing and export inspection. Initial configuration, tool discovery, waiting and panel approval remain part of the user journey; no zero-effort or measured speedup claim is made.

## Original task

Use the owner's [original Chinese prompt](prompt.zh-CN.txt) for reproduction. The following is an English translation for readers, not a second original input:

> Call the LazyMind MCP PPT workflow to create a 16:9 presentation about Beijing's Palace Museum, covering its history, architectural layout, cultural collections, preservation and continuing heritage. Use vermilion, gold and ivory, with a dignified, elegant, classical Chinese style and the feel of a museum catalogue. Collect images, using palace panoramas and artifact close-ups. Keep layouts simple, leave generous space, combine text with images and highlight one core point per slide.

The prompt does not specify a page count. Both historical outputs have four pages, and the Codex recording includes an explicit four-page answer. If asked during reproduction, answer “four pages” and record that clarification separately. Web material and generated images can change; pixel-identical regeneration is not expected.

## Before running

Complete [Agent setup](../shared/agent-setup.md) and use the original task above. It asks the workflow to collect material; no fixed original image input bundle is available. Preserve the actual collected files and attribution for the revision and same-input comparison. The exact DSH local-edit instruction is still missing.

## Reproduction sequence

1. Open a new Codex or DSH task. Provide the original task and original assets; ask it to use the LazyMind PPT Workflow. Let the Agent discover the available workflow instead of copying another installation's ID.
2. Verify the input material is readable. Inspect the collected information, image selections and outline in the Workflow panel. Record a user-requested adjustment and the explicit approval before generation.
3. Wait for the actual run to complete. Save its run/revision reference and export the initial deck as `deck-v1.pptx`. Open it to inspect content, layout and data.
4. Apply the original case's local modification request. Record the selected page/element and all user actions. Do not regenerate the entire deck merely to simulate a local edit.
5. Export `deck-v2.pptx`. Compare every page with V1: the requested change must be present and the untouched pages must retain their content and layout. File hashes alone cannot establish a visual match.
6. Open V2 in a named version of PowerPoint or WPS, edit a text element, save and reopen it. Record which element types were editable; do not call a slide-sized image an editable deck.
7. Check that the final panel result and exported revision agree. Repeat the full path separately for each advertised host.

The V1/V2 filenames above are future output requirements. The historical PDF is available for preview, but its matching PPTX, exact local-edit instruction and separate before/after exports remain missing. The first group's PPTX contains native text and image objects; that structural inspection cannot substitute for an edit/save/reopen test of the selected second deck.

## Evidence and comparison

Follow the [verification record](verification.md). For the direct-Agent baseline, use the same original prompt and assets with the Agent's normal presentation capabilities. Record setup, review, edits, export, waiting and manual work separately. Do not claim an advantage from different input quality or hide the required LazyMind panel interactions.

Add only actual outputs and sanitized evidence after running. Record partial failures and manual fixes. A 60–90 second video may summarize the run, but accelerated waiting or separate runs must be labeled; video duration is not execution time.

## Existing implementation

Reuse the [PPT Workflow](../../workflows/ppt-workflow/README.md). The repository also has an [existing League of Legends preview](../../skills/featured/ppt-workflow/featured.yaml); it is a different case and must not be labeled as the Palace Museum output. This example changes no production workflow or public interface.

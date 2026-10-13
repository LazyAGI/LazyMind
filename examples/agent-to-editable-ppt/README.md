# Use the LazyMind plugin to review and edit a PPT from your Agent

[中文](README.CN.md) · [Examples](../README.md)

**Status: preparation, not a verified reproduction.** This case reuses the existing Codex Palace Museum PPT demonstration. The original prompt, input assets and result files have not yet been supplied. Do not infer them from the video title or replace the case with another topic.

## What this case should demonstrate

Keep using Codex or DSH to start the task. LazyMind provides inspectable intermediate material and a Workflow panel for review and editing, so the user can adjust the work before delivery. The value to verify is the connected process—not simply that an Agent can generate slides.

The references are [Codex + Palace Museum PPT](https://www.bilibili.com/video/BV1TppR6xEj7/) and [DSH + LazyMind plugin](https://www.bilibili.com/video/BV1Xcpt6REjc/). See [source availability](sources.md). Their summaries describe previewing material, images and outlines, followed by slide or element edits. The videos themselves have not been reviewed in this preparation run.

## Before running

Complete [Agent setup](../shared/agent-setup.md). Obtain the original material listed in [sources](sources.md), including the exact task and modification requests. Keep source attribution and permitted redistribution information with the material. Use the same source set for the initial deck and its revision.

## Reproduction sequence

1. Open a new Codex or DSH task. Provide the original task and original assets; ask it to use the LazyMind PPT Workflow. Let the Agent discover the available workflow instead of copying another installation's ID.
2. Verify the input material is readable. Inspect the collected information, image selections and outline in the Workflow panel. Record a user-requested adjustment and the explicit approval before generation.
3. Wait for the actual run to complete. Save its run/revision reference and export the initial deck as `deck-v1.pptx`. Open it to inspect content, layout and data.
4. Apply the original case's local modification request. Record the selected page/element and all user actions. Do not regenerate the entire deck merely to simulate a local edit.
5. Export `deck-v2.pptx`. Compare every page with V1: the requested change must be present and the untouched pages must retain their content and layout. File hashes alone cannot establish a visual match.
6. Open V2 in a named version of PowerPoint or WPS, edit a text element, save and reopen it. Record which element types were editable; do not call a slide-sized image an editable deck.
7. Check that the final panel result and exported revision agree. Repeat the full path separately for each advertised host.

The filenames above are output requirements, not included results. The original complete prompt and edit request remain blocked on source material; this page is not yet a copy-and-run tutorial.

## Evidence and comparison

Follow the [verification record](verification.md). For the direct-Agent baseline, use the same original prompt and assets with the Agent's normal presentation capabilities. Record setup, review, edits, export, waiting and manual work separately. Do not claim an advantage from different input quality or hide the required LazyMind panel interactions.

Add only actual outputs and sanitized evidence after running. Record partial failures and manual fixes. A 60–90 second video may summarize the run, but accelerated waiting or separate runs must be labeled; video duration is not execution time.

## Existing implementation

Reuse the [PPT Workflow](../../workflows/ppt-workflow/README.md). The repository also has an [existing League of Legends preview](../../skills/featured/ppt-workflow/featured.yaml); it is a different case and must not be labeled as the Palace Museum output. This example changes no production workflow or public interface.

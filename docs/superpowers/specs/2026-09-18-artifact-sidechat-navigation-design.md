# Artifact Side-chat Navigation Design

## Goal

Make side chat an exclusive view of the ordinary right sidebar, remove the
conversation-menu branch entry, and preserve the existing workflow, task, and
source-reference panel behavior.

## Scope

- Remove the `fork-from-latest` entry and its handler from the conversation
  overflow menu. Message-level branching remains unchanged.
- When a user opens side chat, clear the conversation-file request before
  mounting the side-chat view. Side chat therefore replaces rather than stacks
  on the file panel.
- When the user closes side chat, do not restore a previous file or task view.
  The ordinary right sidebar closes in that single action.
- Keep source-reference panels and expanded workflow rail behavior unchanged:
  their existing priority and tabs remain authoritative.

## Design

`ChatLayout` owns the ordinary right-sidebar selection state. It currently
keeps an artifact-panel request while side chat opens; `showingArtifacts` then
outranks `showingSideChat`, leaving the side-chat instance hidden under the
file panel. The implementation will make opening side chat reset the artifact
request and reset the ordinary panel width, then collapse tasks as it does
today. This gives the side chat sole ownership of the ordinary right sidebar.

Closing side chat will remove the side-chat state and leave the artifact
request cleared. With no active ordinary panel, the right sidebar is hidden.
It will continue to restore keyboard focus to the initiating control when one
exists, otherwise to the main chat input.

The workflow expanded rail deliberately remains separate: it has only chat,
tasks, and artifacts tabs. Opening a side chat continues to exit that expanded
mode before the ordinary sidebar is shown. Source-reference behavior continues
to clear the file request and takes precedence over ordinary sidebar content.

## Tests

Extend the existing `ChatLayout` integration tests to verify:

1. The overflow menu exposes conversation files and side chat, but no branch
   action.
2. Opening side chat from an active conversation-file view hides the file
   panel and shows the side-chat panel.
3. Closing that side chat hides the whole ordinary right sidebar instead of
   restoring the file panel.
4. Existing tests for artifact-vs-task replacement and source-reference
   precedence remain green, protecting workflow-adjacent behavior.

## Non-goals

- Changing server-side fork capabilities or message-level branch creation.
- Changing the side-chat lifecycle, retention, discard, or streaming APIs.
- Adding a workflow-rail side-chat tab.

# Composer and Settings Hotfix Design

## Goal

Fix four related usability problems without changing backend contracts or expanding the settings information architecture:

1. Keep the composer add-resource button visible when the composer is idle.
2. Align every add-resource menu item and let the compact menu size itself to its contents.
3. Give Developer settings accurate copy and a clear parent-disabled visual state.
4. Direct users from Skills & Plugins settings to Capability Management for lifecycle configuration.

## Scope and approach

This is a targeted frontend hotfix on `fix/hotfix`. It follows existing React, Ant Design, Sass, routing, and localization patterns. The implementation will change only the relevant composer and settings components, their styles, translations, and regression tests.

The hotfix will not alter feature-control APIs, stored preferences, capability routes, or the layout of unrelated composer popovers.

## Composer add-resource control

### Trigger visibility

The add-resource trigger remains a 32 px icon button on pointer devices and a 44 px target on coarse-pointer devices. It is visible in the default, hover, focus, disabled, and expanded states. Hover and expanded background feedback, keyboard focus indication, and the expanded-state icon rotation remain unchanged.

The fine-pointer rule that sets the idle trigger to `opacity: 0` will be removed. The button remains discoverable without requiring hover.

### Compact menu geometry

The add-resource popover is a compact action menu, not a full-height selection panel. It will:

- use its natural content height;
- retain the existing compact border, radius, shadow, and padding;
- stop receiving the viewport-derived `menuHeight` body style;
- stop sharing the 400 px by 420 px rules used by model, featured-capability, and conversation-configuration panels.

The shared `menuHeight` state remains available to those complex panels.

### Item alignment

Each menu action uses a semantic Ant Design icon inside the same 28 px fixed icon box. “Record skill” replaces the bare `◉` character with a recording-related icon. Attachment, optional skill deposit, knowledge base, and prompt template actions use the same icon class so every label starts on the same column.

Disabled menu items retain their current disabled colors and behavior. Menu actions, ordering, tooltips, and callbacks do not change.

## Developer settings

### Page description

The page header will use frontend-localized Developer copy instead of the generic settings fallback returned when the backend overview has no Developer section.

- Chinese: `管理开发者模式及仅在该模式开启时生效的调试选项。`
- English: `Manage Developer Mode and debugging options that take effect only while it is on.`

The master-switch description will explicitly state that turning Developer Mode off pauses dependent features while preserving their saved preferences. The existing `hideEvo` variant continues to omit Algorithm Leap.

### Parent-disabled state

When Developer Mode is off:

- show a lightweight status notice: `开发者模式已关闭；以下选项的设置已保留，但当前不生效。`;
- mark the dependent-options group and both child rows as disabled for assistive technology;
- tint the dependent rows with the existing paused-row neutral background;
- keep the sensitive-word and performance switches disabled;
- retain each switch’s saved checked value so re-enabling Developer Mode restores the prior configuration.

The notice will be connected to the disabled controls with `aria-describedby`. The design will not clear child preferences, place an opaque overlay over the rows, or reduce text contrast across the entire group.

When Developer Mode is enabled, the status notice and paused styling disappear and the child switches become interactive again.

## Skills & Plugins guidance

The Skills & Plugins settings page remains responsible for availability controls: bulk and per-resource enable/disable, search, and pagination. Capability Management remains responsible for creating, installing, editing, and organizing skills and workflows.

The header will add a secondary button beside the existing group control:

- Chinese label: `前往能力管理`
- English label: `Open Capability Management`

The description will be:

- Chinese: `分别启停当前账号的个人技能和工作流；创建、安装、编辑或整理资源，请前往能力管理。`
- English: `Enable or disable personal skills and workflows for the current account independently. To create, install, edit, or organize them, go to Capability Management.`

The button uses normal push navigation so browser Back returns to Settings. Its target follows the active tab:

- Skills: `/memory-management/skills`
- Workflows: `/memory-management/skills?skillView=workflows`

The button stays available during loading, empty, error, and master-disabled states because it is navigation rather than a resource mutation.

## Testing

Implementation follows red-green-refactor.

### Composer regressions

- Assert every resource-menu item uses the shared icon-box class.
- Assert the compact popover receives no inline fixed body height.
- Add a Sass contract test that rejects an idle `opacity: 0` rule for the trigger.
- Assert the full-height composer-panel selector does not include the resource popover.
- Run nearby featured-capability and local-workspace tests to protect the complex panels that still use fixed available height.

### Developer settings regressions

- Assert the page uses Developer-specific description copy rather than the generic fallback.
- With Developer Mode off and saved child preferences on, assert the switches remain checked, disabled, described by the notice, and contained in paused rows.
- After enabling Developer Mode, assert the notice and paused state disappear without losing child preferences.
- Add a bilingual localization contract for new and changed copy.

### Capability guidance regressions

- Assert the Capability Management button is visible across resource states.
- Assert Skills and Workflows tabs generate their respective destinations.
- Assert using the button performs navigation and never calls enable/disable APIs.
- Update the existing bilingual settings-copy contract.

### Verification

Run focused Vitest suites with a single fork worker, frontend copy-contract tests, ESLint for touched files, full TypeScript checking, and a production build. Then launch the stack and visually verify the composer menu and both settings pages at desktop and narrow viewport widths, including keyboard focus behavior.

## Delivery

After verification:

1. Commit the implementation on `fix/hotfix`.
2. Push the branch to the configured fork and open a pull request against `LazyAGI/LazyMind:main`.
3. Archive the temporary worktree so the branch can be checked out elsewhere.
4. Switch the original workspace to `fix/hotfix` while preserving its pre-existing uncommitted files.
5. Start the repository stack using the Makefile’s Compose workflow and report service status and the local URL.


# Create Plugin Skill

## WHEN TO USE

Use this skill **only** when the user explicitly asks to create a **new** plugin.

Trigger conditions:
- "帮我创建一个…插件"
- "新建一个插件"
- "我想做一个插件"
- Any similar request that clearly asks to author a brand-new plugin from scratch.

Do NOT use this skill when:
- The user wants to modify or fix an existing plugin.
- The user is asking about how plugins work (answer directly).
- The user mentions a plugin in passing but is not requesting creation.

---

## WORKFLOW

This skill follows a strict three-step flow. **Do not skip steps or merge them.**

### Step 1 — Generate a plugin summary (no tool call)

Read the user's request carefully. Output a **natural-language summary** in this exact format:

```
插件名称：<display name>
功能描述：<one or two sentences describing what the plugin does>
输入槽位：<slot_id>（<type>，<cardinality>）<label>  — one per line
输出槽位：<slot_id>（<type>，<cardinality>）<label>  — one per line
主要步骤：1. <step_id>  2. <step_id>  3. <step_id>  ...
```

Rules for the summary:
- Slot `type` must be one of: `text`, `image`, `file`, `json`.
- Slot `cardinality` must be `single` or `list`.
- Keep steps to 2–5. Each step id should be a snake_case verb phrase (e.g. `extract_clauses`).
- Be specific enough that the AI generator can produce a working plugin without guessing.

After printing the summary, **immediately** call `ask_user` with:

```json
{
  "questions": [
    {
      "id": "confirm",
      "type": "boolean",
      "text": "以上是插件方案，是否确认创建？如需调整请点击「修改」并说明修改意见"
    }
  ]
}
```

This suspends the turn and waits for user input.

---

### Step 2 — Handle user response

- If the user clicks **Yes / 是 / 确认** (or any affirmative): proceed immediately to Step 3.
- If the user clicks **No / 否 / 修改** or provides corrections: update the summary incorporating the feedback, then call `ask_user` again. Repeat until the user confirms.

---

### Step 3 — Create the plugin draft (call `create_plugin_draft`)

Call `create_plugin_draft` with:
- `name`: the plugin display name from the confirmed summary.
- `description`: a comprehensive description combining the functional description, slot details, and step list from the confirmed summary. Write enough detail for the AI to generate without ambiguity.
- `slots`: the slot lines from the summary, one per line.
- `steps`: the step ids from the summary, one per line.

After the tool returns successfully, write a **brief confirmation message** that:
1. States the plugin is being generated.
2. Includes a Markdown link to the editor using `editor_url` from the tool result:
   `[点击这里打开插件编辑器](<editor_url>)`
3. Notes that generation runs in the background and the user can refresh to see progress.

Do NOT output any additional technical details or YAML.

---

## IMPORTANT CONSTRAINTS

- Never call `create_plugin_draft` before the user confirms (Step 2 must complete first).
- Never call `create_plugin_draft` more than once per conversation request.
- The `ask_user` tool is a stop-tool — after calling it the current turn ends. The user's next message is their answer; resume from Step 2.
- Keep the summary in Chinese for readability, but slot ids and step ids must be English snake_case.

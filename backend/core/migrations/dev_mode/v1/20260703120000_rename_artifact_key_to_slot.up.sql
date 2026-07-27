-- Step 1: 列重命名 + 索引重建

-- sub_agent_artifacts
ALTER TABLE sub_agent_artifacts RENAME COLUMN artifact_key TO slot;
DROP INDEX IF EXISTS idx_saa_task_key;
CREATE INDEX idx_saa_task_slot ON sub_agent_artifacts(task_id, slot, seq);

-- sub_agent_tasks
ALTER TABLE sub_agent_tasks RENAME COLUMN input_artifact_keys  TO input_slots;
ALTER TABLE sub_agent_tasks RENAME COLUMN output_artifact_keys TO output_slots;

-- plugin_slot_revisions
ALTER TABLE plugin_slot_revisions RENAME COLUMN artifact_key TO slot;
DROP INDEX IF EXISTS idx_psr_artifact;
CREATE INDEX idx_psr_slot ON plugin_slot_revisions(slot);

-- plugin_human_artifacts
ALTER TABLE plugin_human_artifacts RENAME COLUMN artifact_key TO slot;
DROP INDEX IF EXISTS idx_plugin_human_artifacts_session_key;
CREATE INDEX idx_plugin_human_artifacts_session_slot
    ON plugin_human_artifacts (session_id, slot);

-- Step 2: image-plugin 存量数据值刷新（artifact_key 旧值 → slot.id 新值）
-- 映射关系来自 plugin.yaml slots[].artifact_key → slots[].id：
--   material_image      → material_images
--   optimized_prompt    → prompt_used
--   enhanced_image_url  → enhanced_image_output
--   generated_image_url → image_output
--   subject_analysis    → subject_analysis（相同，无需刷新）

UPDATE sub_agent_artifacts SET slot = 'material_images'       WHERE slot = 'material_image';
UPDATE sub_agent_artifacts SET slot = 'prompt_used'           WHERE slot = 'optimized_prompt';
UPDATE sub_agent_artifacts SET slot = 'enhanced_image_output' WHERE slot = 'enhanced_image_url';
UPDATE sub_agent_artifacts SET slot = 'image_output'          WHERE slot = 'generated_image_url';

UPDATE plugin_slot_revisions SET slot = 'material_images'       WHERE slot = 'material_image';
UPDATE plugin_slot_revisions SET slot = 'prompt_used'           WHERE slot = 'optimized_prompt';
UPDATE plugin_slot_revisions SET slot = 'enhanced_image_output' WHERE slot = 'enhanced_image_url';
UPDATE plugin_slot_revisions SET slot = 'image_output'          WHERE slot = 'generated_image_url';

UPDATE plugin_human_artifacts SET slot = 'material_images'       WHERE slot = 'material_image';
UPDATE plugin_human_artifacts SET slot = 'prompt_used'           WHERE slot = 'optimized_prompt';
UPDATE plugin_human_artifacts SET slot = 'enhanced_image_output' WHERE slot = 'enhanced_image_url';
UPDATE plugin_human_artifacts SET slot = 'image_output'          WHERE slot = 'generated_image_url';

-- sub_agent_tasks JSON 数组值刷新
UPDATE sub_agent_tasks
SET output_slots = replace(output_slots::text, '"material_image"',       '"material_images"')::json
WHERE output_slots::text LIKE '%material_image%';

UPDATE sub_agent_tasks
SET output_slots = replace(output_slots::text, '"optimized_prompt"',     '"prompt_used"')::json
WHERE output_slots::text LIKE '%optimized_prompt%';

UPDATE sub_agent_tasks
SET output_slots = replace(output_slots::text, '"enhanced_image_url"',   '"enhanced_image_output"')::json
WHERE output_slots::text LIKE '%enhanced_image_url%';

UPDATE sub_agent_tasks
SET output_slots = replace(output_slots::text, '"generated_image_url"',  '"image_output"')::json
WHERE output_slots::text LIKE '%generated_image_url%';

UPDATE sub_agent_tasks
SET input_slots = replace(input_slots::text, '"material_image"',       '"material_images"')::json
WHERE input_slots::text LIKE '%material_image%';

UPDATE sub_agent_tasks
SET input_slots = replace(input_slots::text, '"optimized_prompt"',     '"prompt_used"')::json
WHERE input_slots::text LIKE '%optimized_prompt%';

UPDATE sub_agent_tasks
SET input_slots = replace(input_slots::text, '"enhanced_image_url"',   '"enhanced_image_output"')::json
WHERE input_slots::text LIKE '%enhanced_image_url%';

UPDATE sub_agent_tasks
SET input_slots = replace(input_slots::text, '"generated_image_url"',  '"image_output"')::json
WHERE input_slots::text LIKE '%generated_image_url%';

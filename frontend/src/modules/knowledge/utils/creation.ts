import type { KnowledgeOptions } from "./request";

export function knowledgeCreationDefaults(options: KnowledgeOptions, scene = options.default_scene) {
  return { scene, processing_level: options.default_processing_level, algo_id: options.default_algo_id };
}

export function knowledgeCreationMetadata(name: string, description = "", tags: string[] = []) {
  return {
    name: name.trim(),
    description: description.trim(),
    tags: [...new Set(tags.map(tag => tag.trim()).filter(Boolean))],
  };
}

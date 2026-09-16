import { axiosInstance, BASE_URL } from "@/components/request";

type Envelope<T> = { data: T };
const root = `${BASE_URL}/api/core/learning`;

export interface SchemaField { key:string; type:string; label_i18n_key:string; help_i18n_key:string; required:boolean; editable:boolean }
export interface CachePolicy { default_scope:string; allowed_scopes:string[]; context_sensitive:boolean }
export interface AnalysisConfig { instruction:string; resolution_instruction:string; extraction_prompt_template:string; extraction_repair_template:string; resolution_prompt_template:string; allow_plain_text_single_field:boolean; max_candidates:number; max_document_candidates:number; generated_required_fields:string[]; output_language:string; fallback_pattern:string; fallback_kinds:string[]; language_aliases:Record<string,string>; subject_kind_aliases:Record<string,string> }
export interface LearningCapability { key:string; version:number; name_i18n_key:string; description_i18n_key:string; local_only:boolean; languages:string[]; subject_kinds:string[]; fields:SchemaField[]; provider_pipeline:string[]; allowed_question_types:string[]; default_question_types:string[]; cache_policy:CachePolicy; analysis?:AnalysisConfig }
export interface CapabilityRef { key:string; version:number; enabled:boolean; display_order:number; settings?:Record<string,unknown> }
export interface QuestionType { key:string; version:number; name_i18n_key:string; dynamic:boolean }
export interface CapabilityProfile { key:string; name_i18n_key:string; description_i18n_key:string; capabilities:string[] }
export interface CustomCapabilityProfile { id:string; custom_name:string; description:string; capability_refs_json:string }
export interface LearningCatalog { capabilities:LearningCapability[]; question_types:QuestionType[]; profiles:CapabilityProfile[]; local_available:boolean }
export interface KnowledgeBaseCapability { id:string; dataset_id:string; capability_key:string; capability_version:number; enabled:boolean; display_order:number; settings_json:string }
export interface LearningBook { id:string; name:string; description:string; capability_key:string; question_types_json:string }

export const getLearningCatalog = async () => (await axiosInstance.get<Envelope<LearningCatalog>>(`${root}/catalog`)).data.data;
export const listCapabilityProfiles = async () => (await axiosInstance.get<Envelope<{builtin:CapabilityProfile[];custom:CustomCapabilityProfile[]}>>(`${root}/profiles`)).data.data;
export const createCapabilityProfile = async (name:string,description:string,keys:string[]) => (await axiosInstance.post<Envelope<CustomCapabilityProfile>>(`${root}/profiles`,{name,description,capabilities:keys.map((key,i)=>({key,version:1,enabled:true,display_order:i+1}))})).data.data;
export const getKnowledgeBaseCapabilities = async (datasetId:string) => (await axiosInstance.get<Envelope<{items:KnowledgeBaseCapability[]}>>(`${root}/datasets/${datasetId}/capabilities`)).data.data.items || [];
export const saveKnowledgeBaseCapabilities = async (datasetId:string, refs:string[]|CapabilityRef[]) => (await axiosInstance.put(`${root}/datasets/${datasetId}/capabilities`, { capabilities: refs.map((item, i) => typeof item === "string" ? { key:item, version:1, enabled:true, display_order:i+1 } : item) })).data;
export const resolveLearningContent = async (value:Record<string,unknown>) => (await axiosInstance.post(`${root}/content:resolve`,value,{silentError:true} as never)).data.data;
export const listLearningBooks = async () => (await axiosInstance.get<Envelope<{items:LearningBook[]}>>(`${root}/books`)).data.data.items || [];
export const createLearningBook = async (value:{name:string;description?:string;capability_key:string;question_types:string[]}) => (await axiosInstance.post(`${root}/books`,value)).data.data;
export const putLearningPreset = async (value:Record<string,unknown>) => (await axiosInstance.put(`${root}/presets`,value)).data.data;
export interface LearningPreset { id:string; scope_type:string; scope_id:string; document_revision:string; capability_key:string; normalized_key:string; value_json:string; origin:string; status:string; priority:number; user_edited:boolean }
const normalizePreset=(row:Record<string,unknown>):LearningPreset=>({
  id:String(row.id??row.ID??""),scope_type:String(row.scope_type??row.ScopeType??""),scope_id:String(row.scope_id??row.ScopeID??""),document_revision:String(row.document_revision??row.DocumentRevision??""),capability_key:String(row.capability_key??row.CapabilityKey??""),normalized_key:String(row.normalized_key??row.NormalizedKey??""),value_json:String(row.value_json??row.ValueJSON??"{}"),origin:String(row.origin??row.Origin??""),status:String(row.status??row.Status??""),priority:Number(row.priority??row.Priority??0),user_edited:Boolean(row.user_edited??row.UserEdited),
});
export const listLearningPresets = async (scopeType:string,scopeId:string) => {
  const data=(await axiosInstance.get<Envelope<{items:Record<string,unknown>[] }>>(`${root}/presets`,{params:{scope_type:scopeType,scope_id:scopeId}})).data.data;
  return (data.items||[]).map(normalizePreset);
};
export const updateLearningPreset = async (id:string,value:Record<string,unknown>,priority=0) => normalizePreset((await axiosInstance.patch(`${root}/presets/${id}`,{value,priority})).data.data);
export const deleteLearningPreset = async (id:string) => (await axiosInstance.delete(`${root}/presets/${id}`)).data.data;
export interface PreanalysisTask {id:string;status:string;total:number;completed:number;failed:number;result_json:string;error_message:string}
const normalizeTask=(row:Record<string,unknown>):PreanalysisTask=>({id:String(row.id??row.ID??""),status:String(row.status??row.Status??""),total:Number(row.total??row.Total??0),completed:Number(row.completed??row.Completed??0),failed:Number(row.failed??row.Failed??0),result_json:String(row.result_json??row.ResultJSON??""),error_message:String(row.error_message??row.ErrorMessage??"")});
export interface PreanalysisItemInput { text:string;context?:string;language?:string;subject_kind?:string;segment_id?:string;page?:number;start_offset?:number;end_offset?:number }
export const createPreanalysisTask = async (value:{dataset_id:string;document_id:string;document_revision?:string;capability_keys:string[];analysis_direction?:string;items?:PreanalysisItemInput[]}) => normalizeTask((await axiosInstance.post(`${root}/preanalysis/tasks`,value,{silentError:true} as never)).data.data);
export const runPreanalysisTask = async (id:string) => normalizeTask((await axiosInstance.post(`${root}/preanalysis/tasks/${id}:run`,{},{silentError:true} as never)).data.data);
export const getPreanalysisTask = async (id:string) => normalizeTask((await axiosInstance.get(`${root}/preanalysis/tasks/${id}`,{silentError:true} as never)).data.data);
export const getLatestPreanalysisTask = async (datasetId:string,documentId:string) => { const row=(await axiosInstance.get(`${root}/preanalysis/tasks/latest`,{params:{dataset_id:datasetId,document_id:documentId},silentError:true} as never)).data.data as Record<string,unknown>|null; return row?normalizeTask(row):undefined; };
export const cancelPreanalysisTask = async (id:string) => normalizeTask((await axiosInstance.post(`${root}/preanalysis/tasks/${id}:cancel`,{})).data.data);
export const listPreanalysisDrafts = async (id:string) => { const data=(await axiosInstance.get(`${root}/preanalysis/tasks/${id}/drafts`,{silentError:true} as never)).data.data as Record<string,unknown>; const presets=(data.presets??data.Presets??[]) as Array<Record<string,unknown>>; const contents=(data.contents??data.Contents??[]) as Array<Record<string,unknown>>; return {presets:presets.map(normalizePreset),contents}; };
export const publishPreanalysisDrafts = async (id:string,documentRevision:string,presetIds:string[],contentIds:string[]) => (await axiosInstance.post(`${root}/preanalysis/tasks/${id}/drafts:publish`,{document_revision:documentRevision,preset_ids:presetIds,content_ids:contentIds},{silentError:true} as never)).data.data;
export const importLearningDictionary = async (value:Record<string,unknown>) => (await axiosInstance.post(`${root}/dictionaries:import`,value)).data.data as {imported:number};

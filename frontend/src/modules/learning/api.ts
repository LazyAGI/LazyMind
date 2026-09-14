import { axiosInstance, BASE_URL } from "@/components/request";

type Envelope<T> = { data: T };
const root = `${BASE_URL}/api/core/learning`;

export interface SchemaField { key:string; type:string; label_i18n_key:string; help_i18n_key:string; required:boolean; editable:boolean }
export interface CachePolicy { default_scope:string; allowed_scopes:string[]; context_sensitive:boolean }
export interface LearningCapability { key:string; version:number; name_i18n_key:string; description_i18n_key:string; local_only:boolean; languages:string[]; subject_kinds:string[]; fields:SchemaField[]; provider_pipeline:string[]; allowed_question_types:string[]; default_question_types:string[]; cache_policy:CachePolicy }
export interface CapabilityRef { key:string; version:number; enabled:boolean; display_order:number; settings?:Record<string,unknown> }
export interface QuestionType { key:string; version:number; name_i18n_key:string; dynamic:boolean }
export interface CapabilityProfile { key:string; name_i18n_key:string; description_i18n_key:string; capabilities:string[] }
export interface CustomCapabilityProfile { id:string; custom_name:string; description:string; capability_refs_json:string }
export interface LearningCatalog { capabilities:LearningCapability[]; question_types:QuestionType[]; profiles:CapabilityProfile[]; local_available:boolean }
export interface KnowledgeBaseCapability { id:string; dataset_id:string; capability_key:string; capability_version:number; enabled:boolean; display_order:number; settings_json:string }
export interface LearningBook { id:string; name:string; description:string; capability_key:string; question_types_json:string }
export interface SelectionAnalysis { language:string; subject_kinds:string[]; capability_keys:string[]; books:LearningBook[] }

export const getLearningCatalog = async () => (await axiosInstance.get<Envelope<LearningCatalog>>(`${root}/catalog`)).data.data;
export const listCapabilityProfiles = async () => (await axiosInstance.get<Envelope<{builtin:CapabilityProfile[];custom:CustomCapabilityProfile[]}>>(`${root}/profiles`)).data.data;
export const createCapabilityProfile = async (name:string,description:string,keys:string[]) => (await axiosInstance.post<Envelope<CustomCapabilityProfile>>(`${root}/profiles`,{name,description,capabilities:keys.map((key,i)=>({key,version:1,enabled:true,display_order:i+1}))})).data.data;
export const getKnowledgeBaseCapabilities = async (datasetId:string) => (await axiosInstance.get<Envelope<{items:KnowledgeBaseCapability[]}>>(`${root}/datasets/${datasetId}/capabilities`)).data.data.items || [];
export const saveKnowledgeBaseCapabilities = async (datasetId:string, refs:string[]|CapabilityRef[]) => (await axiosInstance.put(`${root}/datasets/${datasetId}/capabilities`, { capabilities: refs.map((item, i) => typeof item === "string" ? { key:item, version:1, enabled:true, display_order:i+1 } : item) })).data;
export const analyzeLearningSelection = async (datasetId:string,text:string) => (await axiosInstance.post<Envelope<SelectionAnalysis>>(`${root}/selections:analyze`,{dataset_id:datasetId,text})).data.data;
export const resolveLearningContent = async (value:Record<string,unknown>) => (await axiosInstance.post(`${root}/content:resolve`,value,{silentError:true} as never)).data.data;
export const confirmLearningContent = async (contentId:string,value:Record<string,unknown>,bookIds:string[]) => (await axiosInstance.post(`${root}/content/${contentId}:confirm`,{value,book_ids:bookIds})).data.data;
export const listLearningBooks = async () => (await axiosInstance.get<Envelope<{items:LearningBook[]}>>(`${root}/books`)).data.data.items || [];
export const createLearningBook = async (value:{name:string;description?:string;capability_key:string;question_types:string[]}) => (await axiosInstance.post(`${root}/books`,value)).data.data;
export const updateLearningBook = async (id:string,value:{name:string;description?:string;capability_key:string;question_types:string[]}) => (await axiosInstance.patch(`${root}/books/${id}`,value)).data.data;
export const archiveLearningBook = async (id:string) => (await axiosInstance.delete(`${root}/books/${id}`)).data.data;
export const createLearningReviewSession = async (bookId:string,locale:string,limit=20) => (await axiosInstance.post(`${root}/review/sessions`,{book_id:bookId,locale,limit})).data.data;
export const answerLearningQuestion = async (sessionId:string,value:{question_id:string;response?:string;rating?:string;idempotency_key:string}) => (await axiosInstance.post(`${root}/review/sessions/${sessionId}/answers`,value)).data.data;
export const putLearningPreset = async (value:Record<string,unknown>) => (await axiosInstance.put(`${root}/presets`,value)).data.data;
export const createPreanalysisTask = async (value:{dataset_id:string;document_id:string;document_revision?:string;capability_keys:string[]}) => (await axiosInstance.post(`${root}/preanalysis/tasks`,value)).data.data as {id:string;status:string;total:number;completed:number;failed:number;result_json:string};
export const runPreanalysisTask = async (id:string) => (await axiosInstance.post(`${root}/preanalysis/tasks/${id}:run`,{})).data.data as {id:string;status:string;total:number;completed:number;failed:number;result_json:string};
export const getPreanalysisTask = async (id:string) => (await axiosInstance.get(`${root}/preanalysis/tasks/${id}`)).data.data as {id:string;status:string;total:number;completed:number;failed:number;result_json:string};
export const cancelPreanalysisTask = async (id:string) => (await axiosInstance.post(`${root}/preanalysis/tasks/${id}:cancel`,{})).data.data;
export const listPreanalysisDrafts = async (id:string) => (await axiosInstance.get(`${root}/preanalysis/tasks/${id}/drafts`)).data.data as {presets:Array<{id:string;capability_key:string;normalized_key:string;value_json:string}>;contents:Array<{id:string;capability_key:string;content_json:string}>};
export const publishPreanalysisDrafts = async (id:string,documentRevision:string,presetIds:string[],contentIds:string[]) => (await axiosInstance.post(`${root}/preanalysis/tasks/${id}/drafts:publish`,{document_revision:documentRevision,preset_ids:presetIds,content_ids:contentIds})).data.data;
export const importLearningDictionary = async (value:Record<string,unknown>) => (await axiosInstance.post(`${root}/dictionaries:import`,value)).data.data as {imported:number};

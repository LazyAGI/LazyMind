import { axiosInstance, BASE_URL } from "@/components/request";
interface Envelope<T>{data:T}
const root=`${BASE_URL}/api/core/vocabulary`;
export interface VocabularyProviderSetting{selected_provider:"anki"|"local";anki_endpoint:string;anki_deck_name:string;anki_model_version?:number;local_default_wordbook_id?:string;anki_last_sync_at?:string;anki_last_sync_error?:string}
export interface AnkiProviderStatus{provider:"anki";connected:boolean;version?:number;initialized:boolean;deck_name:string;pending_operations:number;message?:string;permission?:string;review_capability?:"full"|"read_only"|"manage_only";last_sync_at?:string;last_sync_error?:string}
export interface AnkiDeck{id:number;name:string;is_default:boolean}
export interface Wordbook{id:string;name:string;description:string;archived_at?:string}
export interface DictionarySense{id:string;part_of_speech:string;definition:string;translation:string}
export interface DictionaryEntry{id:string;term:string;phonetic:string;source_name:string;source_version:string;license_id:string;source_locator:string;senses:DictionarySense[];examples:{sentence:string;translation:string}[]}
export interface VocabularyWord{id:string;term:string;language:string;phonetic:string;part_of_speech:string;meaning:string;definition:string;user_note:string;provider:string;mastered_at?:string;origin_type:string;source_name:string;source_version:string;license_id:string}
export interface VocabularyListItem{word:VocabularyWord;example?:{id:string;sentence:string;translation:string;content_origin:string};state:string;due_at?:string;card_type?:string;tags:string[];wordbooks:Wordbook[];reps:number;lapses:number;row_version?:number;options?:Record<string,string>}
export interface ReviewChoice{value:string;label:string;part_of_speech?:string}
export interface ReviewQuestion extends VocabularyListItem{card_id:string;prompt:string;answer:string;interaction?:"multiple_choice"|"text_input"|"self_assessment";choices?:ReviewChoice[];remaining:number;previewed_at:string;row_version:number;options:Record<string,string>}
export interface ReviewSessionReport{session:{id:string;provider:string;wordbook_name:string};total:number;correct:number;incorrect:number;accuracy:number;rating_counts:Record<string,number>;average_interval_before_days:number;average_interval_after_days:number;difficult_words:string[]}
export interface ActiveReviewSession{active:boolean;session?:{id:string;provider:string;wordbook_id:string;wordbook_name:string;status:string;expires_at:string};remaining?:number}
export interface ReviewStats{total:number;due:number;new:number;learning:number;review:number;relearning:number;mastered:number;weak:number;reviewed_today:number}
export interface ReviewLog{card_id:string;word_id:string;term:string;card_type:string;rating:number;reviewed_at:string;fsrs_log:{review_log?:{ScheduledDays?:number;ElapsedDays?:number};interval_before_days?:number;interval_after_days?:number;due_at?:string;ScheduledDays?:number;ElapsedDays?:number}}
export interface SelectionResolveResult{term:string;sentence:string;provider:"anki"|"local";existing?:VocabularyWord;dictionary:DictionaryEntry[];wordbooks:Wordbook[]}
export interface AddVocabularyWord{provider?:"anki"|"local";term:string;language?:string;phonetic?:string;part_of_speech?:string;meaning?:string;definition?:string;user_note?:string;tags?:string[];sentence?:string;translation?:string;example_tags?:string[];dataset_id?:string;document_id?:string;segment_id?:string;page?:number;bbox?:number[];selected_text?:string;context_sentence?:string;wordbook_ids?:string[];origin_type?:string;dictionary_entry_id?:string;document_revision?:string}
export interface DocumentVocabularyItem extends VocabularyWord{example?:{sentence:string;translation:string};source:{page?:number;segment_id?:string;context_sentence?:string;created_at?:string};source_count:number;can_delete:boolean}
export const getVocabularyProvider=async()=>(await axiosInstance.get<Envelope<VocabularyProviderSetting>>(`${root}/provider`)).data.data;
export const saveVocabularyProvider=async(v:VocabularyProviderSetting)=>(await axiosInstance.put<Envelope<VocabularyProviderSetting>>(`${root}/provider`,v)).data.data;
export const getAnkiStatus=async()=>(await axiosInstance.get<Envelope<AnkiProviderStatus>>(`${root}/providers/anki/status`,{silentError:true} as never)).data.data;
export const initializeAnki=async()=>{await axiosInstance.post(`${root}/providers/anki:initialize`,{})};
export const requestAnkiPermission=async()=>{await axiosInstance.post(`${root}/providers/anki:request-permission`,{})};
export const syncAnki=async()=>{await axiosInstance.post(`${root}/providers/anki:sync`,{})};
export const listAnkiDecks=async()=>(await axiosInstance.get<Envelope<{items:AnkiDeck[]}>>(`${root}/providers/anki/decks`)).data.data.items||[];
export const createAnkiDeck=async(name:string)=>{await axiosInstance.post(`${root}/providers/anki/decks`,{name})};
export const addVocabularyWord=async(v:AddVocabularyWord)=>(await axiosInstance.post<Envelope<{queued:boolean;word:VocabularyWord}>>(`${root}/words`,v)).data.data;
export const listVocabulary=async(provider:string,params:Record<string,string>={})=>(await axiosInstance.get<Envelope<{items:VocabularyListItem[]}>>(`${root}/words`,{params:{provider,...params}})).data.data.items||[];
export const getVocabularyWord=async(id:string)=>(await axiosInstance.get<Envelope<VocabularyListItem>>(`${root}/words/${id}`)).data.data;
export const updateVocabularyWord=async(id:string,v:Record<string,unknown>)=>(await axiosInstance.patch<Envelope<VocabularyWord>>(`${root}/words/${id}`,v)).data.data;
export const nextVocabularyReview=async(provider:string)=>(await axiosInstance.get<Envelope<ReviewQuestion|null>>(`${root}/review/next`,{params:{provider}})).data.data;
export const startVocabularyReviewSession=async(count=5)=>(await axiosInstance.post<Envelope<{session:{id:string};questions:ReviewQuestion[]}>>(`${root}/review/sessions`,{count})).data.data;
export const getActiveVocabularyReviewSession=async()=>(await axiosInstance.get<Envelope<ActiveReviewSession>>(`${root}/review/sessions/active`)).data.data;
export const submitVocabularySessionReview=async(sessionId:string,v:{word_id?:string;term?:string;card_id:string;rating?:string;response?:string;row_version:number;idempotency_key:string;previewed_at:string})=>{await axiosInstance.post(`${root}/review/sessions/${sessionId}/answers`,v)};
export const completeVocabularyReviewSession=async(sessionId:string)=>(await axiosInstance.post<Envelope<ReviewSessionReport>>(`${root}/review/sessions/${sessionId}:complete`,{})).data.data;
export const reviewVocabulary=async(id:string,provider:string,v:{card_id:string;rating:string;row_version:number;idempotency_key:string;previewed_at:string})=>{await axiosInstance.post(`${root}/words/${id||"anki"}:review`,v,{params:{provider}})};
export const masterVocabulary=async(id:string)=>{await axiosInstance.post(`${root}/words/${id}:master`,{})};
export const resumeVocabulary=async(id:string)=>{await axiosInstance.post(`${root}/words/${id}:resume`,{})};
export const resetVocabulary=async(id:string)=>{await axiosInstance.post(`${root}/words/${id}:reset`,{})};
export const resetVocabularyBatch=async(scope:"today"|"wordbook",wordbookId?:string)=>(await axiosInstance.post<Envelope<{reset:number}>>(`${root}/words:reset`,{scope,wordbook_id:wordbookId})).data.data;
export const deleteVocabulary=async(id:string)=>{await axiosInstance.delete(`${root}/words/${id}`)};
export const listWordbooks=async()=>(await axiosInstance.get<Envelope<{items:Wordbook[]}>>(`${root}/wordbooks`)).data.data.items||[];
export const createWordbook=async(v:{name:string;description?:string})=>(await axiosInstance.post<Envelope<Wordbook>>(`${root}/wordbooks`,v)).data.data;
export const updateWordbook=async(id:string,v:Record<string,unknown>)=>(await axiosInstance.patch<Envelope<Wordbook>>(`${root}/wordbooks/${id}`,v)).data.data;
export const deleteWordbook=async(id:string,mode:"move"|"delete_words",targetId?:string)=>{await axiosInstance.delete(`${root}/wordbooks/${id}`,{params:{mode,target_id:targetId}})};
export const deleteAnkiDeck=async(name:string,mode:"move"|"delete_words",target?:string)=>{await axiosInstance.delete(`${root}/providers/anki/decks/${encodeURIComponent(name)}`,{params:{mode,target}})};
export const resolveVocabularySelection=async(v:{term:string;language:string;context:string;provider?:string})=>(await axiosInstance.post<Envelope<SelectionResolveResult>>(`${root}/selection:resolve`,v)).data.data;
export const lookupDictionary=async(term:string)=>(await axiosInstance.get<Envelope<{items:DictionaryEntry[]}>>(`${root}/dictionary:lookup`,{params:{language:"en",term}})).data.data.items||[];
export const getVocabularyStats=async(provider="local")=>(await axiosInstance.get<Envelope<ReviewStats>>(`${root}/stats`,{params:{provider}})).data.data;
export const listDocumentVocabulary=async(id:string)=>(await axiosInstance.get<Envelope<{items:DocumentVocabularyItem[]}>>(`${root}/documents/${encodeURIComponent(id)}/words`)).data.data.items||[];
export const removeDocumentVocabulary=async(documentId:string,wordId:string)=>{await axiosInstance.delete(`${root}/documents/${encodeURIComponent(documentId)}/words/${wordId}`)};
export const deleteDocumentVocabulary=async(documentId:string,wordId:string)=>{await axiosInstance.delete(`${root}/documents/${encodeURIComponent(documentId)}/words/${wordId}/word`)};
export const getFSRSProfile=async()=>(await axiosInstance.get<Envelope<Record<string,unknown>>>(`${root}/fsrs/profile`)).data.data;
export const saveFSRSProfile=async(v:Record<string,unknown>)=>(await axiosInstance.put<Envelope<Record<string,unknown>>>(`${root}/fsrs/profile`,v)).data.data;
export const reviewLogsExportUrl=(provider="local")=>`${root}/review/logs:export?provider=${encodeURIComponent(provider)}`;
export const listReviewLogs=async(provider="local")=>(await axiosInstance.get<Envelope<{items:ReviewLog[]}>>(`${root}/review/logs:export`,{params:{provider}})).data.data.items||[];

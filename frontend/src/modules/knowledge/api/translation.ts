import { axiosInstance, BASE_URL } from "@/components/request";

interface ApiEnvelope<T> { data: T }

export interface TranslationResult {
  translated_text: string;
  source: string;
  target: string;
}

export interface TranslationServiceStatus {
  configured: boolean;
  provider: string;
  document_supported: boolean;
}

export class TranslationUnavailableError extends Error {
  constructor(public readonly reason:"dictionary_not_found"|"service_not_configured"){super(reason)}
}

export function isSingleEnglishWord(text:string):boolean {
  return /^[A-Za-z]+(?:['’-][A-Za-z]+)*$/.test(text.trim());
}

export async function getTranslationStatus(): Promise<boolean> {
	return (await getTranslationServiceStatus()).configured;
}

export async function getTranslationServiceStatus(): Promise<TranslationServiceStatus> {
	const response = await axiosInstance.get<ApiEnvelope<Partial<TranslationServiceStatus>>>(
    `${BASE_URL}/api/core/translation/status`,
    { silentError: true } as never,
  );
	return {
		configured: Boolean(response.data.data?.configured),
		provider: response.data.data?.provider || "",
		document_supported: Boolean(response.data.data?.document_supported),
	};
}

export async function translateText(text: string, target?: string, source?: "en" | "zh"): Promise<TranslationResult> {
  const response = await axiosInstance.post<ApiEnvelope<TranslationResult>>(
    `${BASE_URL}/api/core/translation:translate`,
    { text, target, ...(source ? { source } : {}) },
  );
  return response.data.data;
}

export async function translateSelectionText(text:string, source?:"en"|"zh"):Promise<TranslationResult> {
  const value=text.trim();
  if(!source&&isSingleEnglishWord(value)){
    let items:Array<{source_name:string;senses:Array<{translation:string}>}>=[];
    try{
      const response=await axiosInstance.get<ApiEnvelope<{items:typeof items}>>(`${BASE_URL}/api/core/vocabulary/dictionary:lookup`,{params:{language:"en",term:value},silentError:true} as never);
      items=response.data.data?.items||[];
    }catch{/* Fall through to the configured translation provider. */}
    const entry=items.find(item=>item.senses?.some(sense=>sense.translation?.trim()));
    const translated=entry?.senses?.find(sense=>sense.translation?.trim())?.translation?.replace(/\\n/g,"\n").trim();
    if(translated)return{translated_text:translated,source:`dictionary:${entry?.source_name||"built-in"}`,target:"zh"};
    if(!await getTranslationStatus())throw new TranslationUnavailableError("dictionary_not_found");
  }else if(!await getTranslationStatus()){
    throw new TranslationUnavailableError("service_not_configured");
  }
  return translateText(value,source?(source==="zh"?"en":"zh"):undefined,source);
}

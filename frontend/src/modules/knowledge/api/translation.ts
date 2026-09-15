import { axiosInstance, BASE_URL } from "@/components/request";

interface ApiEnvelope<T> { data: T }

export interface TranslationResult {
  translated_text: string;
  source: string;
  target: string;
}

export class TranslationUnavailableError extends Error {
  constructor(public readonly reason:"dictionary_not_found"|"service_not_configured"){super(reason)}
}

export function isSingleEnglishWord(text:string):boolean {
  return /^[A-Za-z]+(?:['’-][A-Za-z]+)*$/.test(text.trim());
}

export async function getTranslationStatus(): Promise<boolean> {
  const response = await axiosInstance.get<ApiEnvelope<{ configured: boolean }>>(
    `${BASE_URL}/api/core/translation/status`,
    { silentError: true } as never,
  );
  return Boolean(response.data.data?.configured);
}

export async function translateText(text: string): Promise<TranslationResult> {
  const response = await axiosInstance.post<ApiEnvelope<TranslationResult>>(
    `${BASE_URL}/api/core/translation:translate`,
    { text },
  );
  return response.data.data;
}

export async function translateSelectionText(text:string):Promise<TranslationResult> {
  const value=text.trim();
  if(isSingleEnglishWord(value)){
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
  return translateText(value);
}

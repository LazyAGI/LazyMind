import { axiosInstance, BASE_URL } from "@/components/request";

interface ApiEnvelope<T> { data: T }

export interface TranslationResult {
  translated_text: string;
  source: string;
  target: string;
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

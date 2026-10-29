import { axiosInstance, BASE_URL } from "@/components/request";
export type CloudVocabularyProvider = "eudic" | "maimemo";
export interface CloudConnection {
  provider: CloudVocabularyProvider;
  configured: boolean;
  enabled: boolean;
  target_id: string;
  target_name: string;
  add_to_study: boolean;
  pending: number;
  failed: number;
  succeeded: number;
  last_error: string;
}
export interface CloudWordbook { id: string; name: string }
export interface CloudOperation { id: string; provider: string; term: string; status: string; last_error: string; updated_at: string }
export interface CloudConnectionInput { token?: string; enabled: boolean; target_id: string; target_name: string; add_to_study: boolean }
interface Envelope<T> { data: T }
const root = `${BASE_URL}/api/core/vocabulary/cloud`;
const quiet = { silentError: true } as never;
export const listCloudConnections = async () => (await axiosInstance.get<Envelope<{items: CloudConnection[]}>>(`${root}/connections`, quiet)).data.data.items || [];
export const listCloudOperations = async () => (await axiosInstance.get<Envelope<{items: CloudOperation[]}>>(`${root}/operations`, quiet)).data.data.items || [];
export const saveCloudConnection = async (provider: CloudVocabularyProvider, body: CloudConnectionInput) => (await axiosInstance.put<Envelope<CloudConnection>>(`${root}/connections/${provider}`, body, quiet)).data.data;
export const testCloudConnection = async (provider: CloudVocabularyProvider, token?: string) => (await axiosInstance.post<Envelope<{items: CloudWordbook[]}>>(`${root}/connections/${provider}/test`, token ? {token} : {}, quiet)).data.data.items || [];
export const createCloudWordbook = async (provider: CloudVocabularyProvider, name: string, token?: string) => (await axiosInstance.post<Envelope<CloudWordbook>>(`${root}/connections/${provider}/wordbooks`, {name, ...(token ? {token} : {})}, quiet)).data.data;
export const disconnectCloudConnection = async (provider: CloudVocabularyProvider) => { await axiosInstance.delete(`${root}/connections/${provider}`, quiet); };
export const syncCloudVocabulary = async (provider: CloudVocabularyProvider) => (await axiosInstance.post<Envelope<{queued:number}>>(`${root}/connections/${provider}/sync`, {}, quiet)).data.data;

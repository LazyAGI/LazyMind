import { Configuration, DocumentDirectoriesApi } from '@/api/generated/core-client';
import type { DocumentDirectoryCommand } from '@/api/generated/core-client';
import { axiosInstance, BASE_URL } from '@/components/request';
export type { DocumentDirectory, DocumentDirectoryDetail, DocumentDirectoryEntry, DocumentDirectoryBatch, DirectoryMail, DocumentFolderNode } from '@/api/generated/core-client';
const client = new DocumentDirectoriesApi(new Configuration({ basePath: BASE_URL }), BASE_URL, axiosInstance);
export async function listDirectories() { return (await client.listDocumentDirectories()).data.data.directories; }
export async function getDirectory(directoryId: string, page: {offset?: number; limit?: number; groupId?: string; query?: string; state?: string} = {}) { return (await client.getDocumentDirectory({ directoryId, ...page })).data.data; }
export async function getSourceNodes(directoryId: string, parentKey: string, offset: number) { return (await client.getDocumentFolderNodes({ directoryId, parentKey, offset })).data.data; }
export async function listBatches() { return (await client.listDocumentDirectoryBatches()).data.data.batches; }
export async function directoryAction(input: DocumentDirectoryCommand) { return (await client.executeDocumentDirectoryAction({ documentDirectoryCommand: input })).data.data; }

export async function readDirectoryMail(directoryId: string, entryId: string, signal?: AbortSignal) { return (await client.readDocumentDirectoryMail({ directoryId, entryId }, { signal, silentError: true } as import("axios").AxiosRequestConfig)).data.data; }

export async function readDirectoryLocalFile(directoryId: string, entryId: string, signal?: AbortSignal) { return (await client.readDocumentDirectoryLocalFile({ directoryId, entryId }, { signal, silentError: true } as import("axios").AxiosRequestConfig)).data.data; }

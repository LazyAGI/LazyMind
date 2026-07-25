import {
  Configuration,
  DefaultApiFactory,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";

export type IdentityAvatarKind = "soul" | "profile";

const identityAvatarApi = DefaultApiFactory(
  new Configuration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

export async function getIdentityAvatar(
  kind: IdentityAvatarKind,
): Promise<Blob> {
  const response =
    kind === "soul"
      ? await identityAvatarApi.apiCoreMemorySoulAvatarGet({
          responseType: "blob",
          silentError: true,
        } as never)
      : await identityAvatarApi.apiCoreMemoryProfileAvatarGet({
          responseType: "blob",
          silentError: true,
        } as never);
  const data = response.data as unknown;
  if (data instanceof Blob) {
    return data;
  }
  return new Blob([data as BlobPart], {
    type: response.headers["content-type"],
  });
}

export async function putIdentityAvatar(
  kind: IdentityAvatarKind,
  file: File,
): Promise<void> {
  if (kind === "soul") {
    await identityAvatarApi.apiCoreMemorySoulAvatarPut({ file });
    return;
  }
  await identityAvatarApi.apiCoreMemoryProfileAvatarPut({ file });
}

export async function deleteIdentityAvatar(
  kind: IdentityAvatarKind,
): Promise<void> {
  if (kind === "soul") {
    await identityAvatarApi.apiCoreMemorySoulAvatarDelete();
    return;
  }
  await identityAvatarApi.apiCoreMemoryProfileAvatarDelete();
}

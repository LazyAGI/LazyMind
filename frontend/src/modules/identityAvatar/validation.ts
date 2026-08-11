export const IDENTITY_AVATAR_MAX_BYTES = 2 * 1024 * 1024;
export const IDENTITY_AVATAR_ACCEPT =
  "image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp";

const supportedTypes = new Set(["image/png", "image/jpeg", "image/webp"]);

export type IdentityAvatarValidationReason =
  | "empty"
  | "type"
  | "size";

export class IdentityAvatarValidationError extends Error {
  constructor(public readonly reason: IdentityAvatarValidationReason) {
    super(`Invalid identity avatar: ${reason}`);
    this.name = "IdentityAvatarValidationError";
  }
}

export function validateIdentityAvatarFile(file: File): void {
  if (file.size === 0) {
    throw new IdentityAvatarValidationError("empty");
  }
  if (!supportedTypes.has(file.type.toLowerCase())) {
    throw new IdentityAvatarValidationError("type");
  }
  if (file.size > IDENTITY_AVATAR_MAX_BYTES) {
    throw new IdentityAvatarValidationError("size");
  }
}

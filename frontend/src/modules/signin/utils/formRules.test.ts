import { describe, expect, it } from "vitest";
import {
  PASSWORD_RULE_MESSAGE,
  USERNAME_MAX_LENGTH,
  USERNAME_MAX_MESSAGE,
  USERNAME_RULE_MESSAGE,
  validatePassword,
  validateUsername,
} from "./formRules";

describe("validateUsername", () => {
  it("resolves for empty/undefined values (required check is delegated elsewhere)", async () => {
    await expect(validateUsername()).resolves.toBeUndefined();
    await expect(validateUsername("")).resolves.toBeUndefined();
  });

  it("resolves for valid usernames that start/end with alphanumerics", async () => {
    await expect(validateUsername("ab")).resolves.toBeUndefined();
    await expect(validateUsername("user.name_1@#-x")).resolves.toBeUndefined();
    await expect(validateUsername("a1")).resolves.toBeUndefined();
  });

  it("rejects usernames shorter than 2 characters", async () => {
    await expect(validateUsername("a")).rejects.toThrow(USERNAME_RULE_MESSAGE);
  });

  it("rejects usernames that start or end with a special character", async () => {
    await expect(validateUsername(".abc")).rejects.toThrow(USERNAME_RULE_MESSAGE);
    await expect(validateUsername("abc.")).rejects.toThrow(USERNAME_RULE_MESSAGE);
    await expect(validateUsername("-abc-")).rejects.toThrow(USERNAME_RULE_MESSAGE);
  });

  it("rejects usernames containing disallowed characters", async () => {
    await expect(validateUsername("ab cd")).rejects.toThrow(USERNAME_RULE_MESSAGE);
    await expect(validateUsername("ab$cd")).rejects.toThrow(USERNAME_RULE_MESSAGE);
    await expect(validateUsername("用户名")).rejects.toThrow(USERNAME_RULE_MESSAGE);
  });

  it("rejects usernames exceeding the max length before checking the regex", async () => {
    const tooLong = "a".repeat(USERNAME_MAX_LENGTH + 1);
    await expect(validateUsername(tooLong)).rejects.toThrow(USERNAME_MAX_MESSAGE);
  });

  it("accepts a username exactly at the max length", async () => {
    const maxLength = `a${"b".repeat(USERNAME_MAX_LENGTH - 2)}c`;
    expect(maxLength.length).toBe(USERNAME_MAX_LENGTH);
    await expect(validateUsername(maxLength)).resolves.toBeUndefined();
  });
});

describe("validatePassword", () => {
  it("resolves for empty/undefined values (required check is delegated elsewhere)", async () => {
    await expect(validatePassword()).resolves.toBeUndefined();
    await expect(validatePassword("")).resolves.toBeUndefined();
  });

  it("resolves for a password satisfying all complexity rules", async () => {
    await expect(validatePassword("Abcdef1@")).resolves.toBeUndefined();
  });

  it("rejects passwords shorter than 8 characters", async () => {
    await expect(validatePassword("Ab1@cd")).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });

  it("rejects passwords longer than 32 characters", async () => {
    const tooLong = `Ab1@${"x".repeat(30)}`;
    await expect(validatePassword(tooLong)).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });

  it("rejects passwords missing an uppercase letter", async () => {
    await expect(validatePassword("abcdef1@")).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });

  it("rejects passwords missing a lowercase letter", async () => {
    await expect(validatePassword("ABCDEF1@")).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });

  it("rejects passwords missing a digit", async () => {
    await expect(validatePassword("Abcdefg@")).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });

  it("rejects passwords missing a special character", async () => {
    await expect(validatePassword("Abcdefg1")).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });

  it("rejects passwords containing characters outside the allowed set", async () => {
    await expect(validatePassword("Abcdef1!")).rejects.toThrow(PASSWORD_RULE_MESSAGE);
  });
});

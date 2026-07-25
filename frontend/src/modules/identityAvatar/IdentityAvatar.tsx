import { RobotOutlined, UserOutlined } from "@ant-design/icons";
import { Spin } from "antd";
import { useEffect } from "react";
import type { CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { IdentityAvatarKind } from "./api";
import { useIdentityAvatarStore } from "./store";
import "./index.scss";

export interface IdentityAvatarProps {
  kind: IdentityAvatarKind;
  size?: number;
  alt?: string;
  className?: string;
}

export default function IdentityAvatar({
  kind,
  size = 32,
  alt,
  className = "",
}: IdentityAvatarProps) {
  const { t } = useTranslation();
  const entry = useIdentityAvatarStore((state) => state.avatars[kind]);
  const load = useIdentityAvatarStore((state) => state.load);
  const markImageError = useIdentityAvatarStore(
    (state) => state.markImageError,
  );
  const syncUser = useIdentityAvatarStore((state) => state.syncUser);

  useEffect(() => {
    syncUser();
    void load(kind);
  }, [kind, load, syncUser]);

  const label =
    alt ||
    t(
      kind === "soul"
        ? "identityAvatar.agentAlt"
        : "identityAvatar.userAlt",
    );
  const style = {
    "--identity-avatar-size": `${size}px`,
  } as CSSProperties;

  return (
    <span
      aria-label={label}
      className={`identity-avatar is-${kind} ${className}`.trim()}
      role="img"
      style={style}
    >
      {entry.url && ["ready", "loading"].includes(entry.status) ? (
        <img
          alt={label}
          src={entry.url}
          onError={() => markImageError(kind)}
        />
      ) : entry.status === "loading" ? (
        <Spin size="small" />
      ) : kind === "soul" ? (
        <RobotOutlined aria-hidden />
      ) : (
        <UserOutlined aria-hidden />
      )}
    </span>
  );
}

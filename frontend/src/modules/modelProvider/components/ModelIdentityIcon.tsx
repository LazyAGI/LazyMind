import { getModelIdentity } from "../modelIdentity";
import "./ModelIdentityIcon.scss";

interface ModelIdentityIconProps {
  modelName: string;
  logoUrl?: string;
  fallbackVendor?: string;
  className?: string;
  size?: number;
}

export function ModelIdentityIcon({
  modelName,
  logoUrl,
  fallbackVendor,
  className,
  size = 24,
}: ModelIdentityIconProps) {
  const identity = getModelIdentity(modelName, fallbackVendor);
  return (
    <span
      className={`model-identity-icon${className ? ` ${className}` : ""}`}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <img
        src={logoUrl || identity.logoUrl}
        alt=""
        width={size - 4}
        height={size - 4}
        draggable={false}
      />
    </span>
  );
}

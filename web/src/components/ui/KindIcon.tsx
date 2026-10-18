import { useSchemaLookup } from "../../hooks";
import { useI18n, type MessageKey } from "../../i18n";
import { isKnownRole, kindOf, type Kind } from "../../inventory/kind";
import { Icon } from "./icons";

/** The icon of what an object is, in the hue of its family (network, compute, storage, power…). */
export function KindIcon({ kind, size = "md" }: { kind: Kind; size?: "md" | "lg" }) {
  return (
    <span className={`kind-icon kind-${kind.family}${size === "lg" ? " kind-icon-lg" : ""}`} data-kind={kind.key}>
      <Icon name={kind.icon} />
    </span>
  );
}

/** The word for what an object is: its role when it is a device with one, its type otherwise. */
export function useKindLabel(): (obj: { type: string; attributes?: Record<string, unknown> }) => { kind: Kind; label: string; typeLabel: string } {
  const { t } = useI18n();
  const lookup = useSchemaLookup();
  return (obj) => {
    const kind = kindOf(obj, lookup.typeDoc(obj.type)?.layer);
    const typeLabel = lookup.typeLabel(obj.type);
    const label = kind.role ? (isKnownRole(kind.role) ? t((kind.role === "other" ? "role.otherDevice" : `role.${kind.role}`) as MessageKey) : kind.role) : typeLabel;
    return { kind, label, typeLabel };
  };
}

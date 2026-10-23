import { Building2, DoorOpen, Eye, ShieldCheck, UserRound } from "lucide-react";
import { whoCanPublishText, whoCanReadText, yourRoleText, type PolicyInput } from "@/lib/adapters/collective";

/**
 * How the collective works, from the viewer's side: who publishes, who reads,
 * and where the viewer stands. The words restate what the server decided
 * (`acceptance_mode`, `data_access`, `your_role`, `can_read`); they decide
 * nothing. Each chip names its rule for a screen reader, since the icon is
 * the only visible cue.
 */
export default function PolicyChips({ policy }: { policy: PolicyInput }) {
  const publishIcon =
    policy.acceptanceMode === "curated" ? (
      <ShieldCheck aria-hidden="true" />
    ) : policy.acceptanceMode === "verified_only" ? (
      <Building2 aria-hidden="true" />
    ) : (
      <DoorOpen aria-hidden="true" />
    );
  const chips = [
    { key: "publish", rule: "who can publish", icon: publishIcon, text: whoCanPublishText(policy) },
    { key: "read", rule: "who can read", icon: <Eye aria-hidden="true" />, text: whoCanReadText(policy) },
    { key: "role", rule: "your role", icon: <UserRound aria-hidden="true" />, text: yourRoleText(policy) },
  ];
  return (
    <ul className="on-rules" aria-label="how this collective works">
      {chips.map((chip) => (
        <li key={chip.key} className="on-rule" data-rule={chip.key}>
          {chip.icon}
          <span>
            <span className="sr-only">{chip.rule}: </span>
            {chip.text}
          </span>
        </li>
      ))}
    </ul>
  );
}

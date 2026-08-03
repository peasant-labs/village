"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, X, Clock } from "lucide-react";
import { Button } from "@/lib/ft-ui";
import { api } from "@/lib/api";

export interface PendingReview {
  groupId: string;
  groupName: string;
}

export default function PendingApprovalBar({
  transcriptId,
  reviews,
}: {
  transcriptId: string;
  reviews: PendingReview[];
}) {
  const qc = useQueryClient();
  const [resolved, setResolved] = useState<Record<string, "approved" | "rejected">>({});

  const review = useMutation({
    mutationFn: ({ groupId, status }: { groupId: string; status: "approved" | "rejected" }) =>
      api(`/groups/${groupId}/shares/${transcriptId}`, {
        method: "PATCH",
        body: JSON.stringify({ status }),
      }),
    onSuccess: (_data, vars) => {
      setResolved((prev) => ({ ...prev, [vars.groupId]: vars.status }));
      qc.invalidateQueries({ queryKey: ["transcript", transcriptId] });
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["group-pending", vars.groupId] });
    },
  });

  const pending = reviews.filter((r) => !resolved[r.groupId]);
  if (pending.length === 0 && Object.keys(resolved).length === 0) return null;

  return (
    <div className="sticky top-0 z-50 border-b border-warning/40 bg-warning-soft">
      <div className="max-w-[1600px] mx-auto px-6 py-2.5 flex flex-col gap-2">
        {pending.map((r) => (
          <div key={r.groupId} className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 min-w-0">
              <Clock className="size-3.5 text-warning shrink-0" />
              <span className="text-[13px] text-ink truncate">
                Pending review in{" "}
                <span className="font-mono text-warning">{r.groupName}</span>
              </span>
            </div>
            <div className="flex items-center gap-2 shrink-0">
              <Button
                variant="primary"
                size="sm"
                icon={Check}
                onClick={() => review.mutate({ groupId: r.groupId, status: "approved" })}
                disabled={review.isPending}
              >
                Approve
              </Button>
              <Button
                variant="ghost"
                size="sm"
                icon={X}
                onClick={() => review.mutate({ groupId: r.groupId, status: "rejected" })}
                disabled={review.isPending}
              >
                Reject
              </Button>
            </div>
          </div>
        ))}
        {reviews
          .filter((r) => resolved[r.groupId])
          .map((r) => (
            <div key={r.groupId} className="flex items-center gap-2 text-[13px] text-ink-3">
              {resolved[r.groupId] === "approved" ? (
                <Check className="size-3.5 text-success" />
              ) : (
                <X className="size-3.5 text-danger" />
              )}
              <span className="truncate">
                <span className="font-mono">{r.groupName}</span> —{" "}
                {resolved[r.groupId] === "approved" ? "approved" : "rejected"}
              </span>
            </div>
          ))}
      </div>
    </div>
  );
}

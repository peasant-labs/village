import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { Attestation } from "../types";

export function useTranscriptAttestations(id: string) {
  return useQuery({
    queryKey: ["attestations", id],
    queryFn: () => api<Attestation[]>(`/transcripts/${id}/attestations`),
    enabled: !!id,
  });
}

export function useCreateAttestation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      transcriptId,
      org_login,
      attestation_type,
      note,
    }: {
      transcriptId: string;
      org_login: string;
      attestation_type: string;
      note?: string;
    }) =>
      api<Attestation>(`/transcripts/${transcriptId}/attestations`, {
        method: "POST",
        body: JSON.stringify({ org_login, attestation_type, note }),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["attestations", vars.transcriptId] });
      qc.invalidateQueries({ queryKey: ["transcript", vars.transcriptId] });
      qc.invalidateQueries({ queryKey: ["transcripts"] });
    },
  });
}

export function useDeleteAttestation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      transcriptId,
      attestationId,
    }: {
      transcriptId: string;
      attestationId: string;
    }) =>
      api(`/transcripts/${transcriptId}/attestations/${attestationId}`, {
        method: "DELETE",
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["attestations", vars.transcriptId] });
      qc.invalidateQueries({ queryKey: ["transcript", vars.transcriptId] });
      qc.invalidateQueries({ queryKey: ["transcripts"] });
    },
  });
}

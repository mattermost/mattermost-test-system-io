import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { API_URL } from '@/services/api';
import type {
  CursorPage,
  Observation,
  Quarantine,
  TriageHealth,
  TriageHealthRow,
  TriageIdentity,
  TriageVerdict,
} from '@/types/triage';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_URL}/triage${path}`, { credentials: 'include', ...init });
  if (!response.ok) {
    const body: unknown = await response.json().catch(() => null);
    const message =
      body && typeof body === 'object' && 'message' in body && typeof body.message === 'string'
        ? body.message
        : response.statusText;
    throw new Error(`${response.status}: ${message}`);
  }
  return response.json() as Promise<T>;
}
export function useTriageHealth(filters: URLSearchParams) {
  return useQuery({
    queryKey: ['triage', 'health', filters.toString()],
    queryFn: () => request<CursorPage<TriageHealth>>(`/health?${filters}`),
    staleTime: 30_000,
  });
}
export function useTriageIdentity(id: string) {
  return useQuery({
    queryKey: ['triage', 'identity', id],
    queryFn: () =>
      request<{
        identity: TriageIdentity;
        health: TriageHealthRow[];
        active_quarantine: Quarantine[];
      }>(`/tests/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
  });
}
export function useTriageObservations(id: string, cursor: string) {
  return useQuery({
    queryKey: ['triage', 'observations', id, cursor],
    queryFn: () =>
      request<CursorPage<Observation>>(
        `/tests/${encodeURIComponent(id)}/observations?limit=50&cursor=${encodeURIComponent(cursor)}`,
      ),
    enabled: Boolean(id),
  });
}
export function useTriageVerdict(id: string) {
  return useQuery({
    queryKey: ['triage', 'verdict', id],
    queryFn: () => request<TriageVerdict>(`/verdicts/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
  });
}
export function useQuarantineMutation() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (
      input:
        | { release: string }
        | {
            identity_id: string;
            lane: string | null;
            base_ref: string;
            issue_url?: string;
            reason: 'manual';
          },
    ) =>
      'release' in input
        ? request<Quarantine>(`/quarantine/${encodeURIComponent(input.release)}`, {
            method: 'DELETE',
          })
        : request<Quarantine>('/quarantine', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(input),
          }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['triage'] });
    },
  });
}

import { fetchJSON, postJSON } from './api';

export interface SessionSummary {
  id: string;
  title: string;
  preview: string;
  turns: number;
  provider: string;
  model: string;
  updatedAt: string;
}

export interface SessionMessage {
  role: 'user' | 'assistant' | 'tool';
  content: string;
  toolName?: string;
  toolInput?: Record<string, unknown> | string;
  toolResult?: string;
  isError?: boolean;
  timestamp: string;
}

export interface Session {
  id: string;
  title: string;
  messages: SessionMessage[];
  provider: string;
  model: string;
  createdAt: string;
  updatedAt: string;
  turns: number;
}

export async function listSessions(workspace?: string): Promise<SessionSummary[]> {
  const params = workspace ? `?workspace=${encodeURIComponent(workspace)}` : '';
  // Backend returns JSON `null` for an empty workspace; the typed return is
  // an array, so coerce once here for every caller.
  const data = await fetchJSON<SessionSummary[] | null>(`/api/sessions${params}`);
  return data ?? [];
}

export async function getSession(id: string): Promise<Session> {
  return fetchJSON(`/api/sessions/${id}`);
}

export async function saveSession(session: Partial<Session>): Promise<{ ok: boolean; id: string }> {
  return postJSON('/api/sessions', session);
}

export async function deleteSession(id: string): Promise<void> {
  await fetchJSON(`/api/sessions/${id}`, { method: 'DELETE' });
}

export async function renameSession(id: string, title: string): Promise<void> {
  await fetchJSON(`/api/sessions/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
  });
}

"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { ExternalLink, Eye, MessageSquareText, RefreshCw, Trash2, Wrench } from "lucide-react";
import { apiRequest } from "../api";
import type { CachedChatMessage, SessionChatResponse, SessionItem, SessionsResponse } from "../types";
import { SectionTitle, StatCard } from "./primitives";

function formatSessionTime(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function truncate(value: string | undefined, max = 120) {
  const text = (value || "").trim();
  if (!text) return "-";
  return text.length > max ? `${text.slice(0, max)}…` : text;
}

function safeStringify(value: unknown) {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function roleTone(role: string) {
  switch (role) {
    case "assistant":
      return "primary";
    case "user":
      return "success";
    case "system":
      return "warning";
    case "tool":
      return "danger";
    default:
      return "primary";
  }
}

function MessageBubble({ message }: { message: CachedChatMessage }) {
  const toolCalls = message.tool_calls || [];
  const alignRight = message.role === "user";
  return (
    <div className={`flex ${alignRight ? "justify-end" : "justify-start"}`}>
      <div className="max-w-[82%] rounded-xl border border-[var(--border)] bg-[var(--surface-hover)] p-4 shadow-sm">
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <span className={`admin-tag ${roleTone(message.role)}`}>{message.role || "message"}</span>
          {message.metadata?.name ? <span className="admin-tag primary">{String(message.metadata.name)}</span> : null}
          {message.metadata?.tool_call_id ? <span className="admin-tag danger">tool result</span> : null}
        </div>
        {message.reasoning_content ? (
          <details className="mb-3 rounded-lg border border-[var(--border)] bg-[var(--surface)] p-3 text-xs text-[var(--text-secondary)]">
            <summary className="cursor-pointer font-semibold">reasoning_content</summary>
            <pre className="mt-2 whitespace-pre-wrap break-words">{message.reasoning_content}</pre>
          </details>
        ) : null}
        <div className="whitespace-pre-wrap break-words text-sm leading-6 text-[var(--text)]">
          {message.content || (toolCalls.length ? "[tool calls]" : "[empty]")}
        </div>
        {toolCalls.length ? (
          <div className="mt-3 flex flex-col gap-2">
            {toolCalls.map((call, index) => (
              <details key={`${message.id}-tool-${index}`} className="rounded-lg border border-[var(--border)] bg-[var(--surface)] p-3 text-xs">
                <summary className="flex cursor-pointer items-center gap-2 font-semibold text-[var(--text-secondary)]">
                  <Wrench size={14} /> Tool call #{index + 1}
                </summary>
                <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-words text-[var(--text)]">{safeStringify(call)}</pre>
              </details>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function SessionsTab({ apiKey }: { apiKey: string }) {
  const [sessions, setSessions] = useState<SessionItem[]>([]);
  const [selectedHash, setSelectedHash] = useState("");
  const [selectedChat, setSelectedChat] = useState<SessionChatResponse | null>(null);
  const [loadingList, setLoadingList] = useState(false);
  const [loadingChat, setLoadingChat] = useState(false);
  const [error, setError] = useState("");

  const selectedSession = useMemo(
    () => sessions.find((session) => session.context_hash === selectedHash) || null,
    [sessions, selectedHash],
  );
  const totalMessages = useMemo(
    () => sessions.reduce((sum, session) => sum + (session.message_count || 0), 0),
    [sessions],
  );
  const sessionsWithTools = useMemo(
    () => sessions.filter((session) => session.has_tools).length,
    [sessions],
  );

  const loadSessions = useCallback(async () => {
    if (!apiKey) return;
    try {
      setLoadingList(true);
      setError("");
      const response = await apiRequest<SessionsResponse>("/api/sessions", {}, apiKey);
      const data = response.sessions || [];
      setSessions(data);
      if (selectedHash && !data.some((session) => session.context_hash === selectedHash)) {
        setSelectedHash("");
        setSelectedChat(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load sessions");
    } finally {
      setLoadingList(false);
    }
  }, [apiKey, selectedHash]);

  const loadChat = useCallback(
    async (session: SessionItem) => {
      if (!apiKey || !session.context_hash) return;
      try {
        setLoadingChat(true);
        setError("");
        setSelectedHash(session.context_hash);
        const params = new URLSearchParams({ context_hash: session.context_hash });
        const response = await apiRequest<SessionChatResponse>(`/api/sessions/chat?${params.toString()}`, {}, apiKey);
        setSelectedChat(response);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load chat");
      } finally {
        setLoadingChat(false);
      }
    },
    [apiKey],
  );

  async function deleteSelectedSession() {
    if (!apiKey || !selectedSession) return;
    try {
      setError("");
      await apiRequest("/api/sessions", {
        method: "DELETE",
        body: JSON.stringify({ context_hash: selectedSession.context_hash }),
      }, apiKey);
      setSelectedHash("");
      setSelectedChat(null);
      await loadSessions();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete session");
    }
  }

  async function clearExpiredSessions() {
    if (!apiKey) return;
    try {
      setError("");
      await apiRequest("/api/sessions/clear-expired", { method: "POST", body: JSON.stringify({}) }, apiKey);
      await loadSessions();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to clear expired sessions");
    }
  }

  useEffect(() => {
    void loadSessions();
  }, [loadSessions]);

  return (
    <div className="flex flex-col gap-6">
      <div className="admin-card">
        <div className="admin-card-header">
          <SectionTitle
            title="Cached Chats"
            description="Proxy-cached Qwen conversation sessions and message snapshots."
            action={
              <div className="flex flex-wrap justify-end gap-2">
                <button className="admin-btn admin-btn-secondary" disabled={loadingList} onClick={() => void clearExpiredSessions()}>
                  <Trash2 size={16} /> Clear expired
                </button>
                <button className="admin-btn admin-btn-primary" disabled={loadingList} onClick={() => void loadSessions()}>
                  <RefreshCw size={16} className={loadingList ? "animate-spin" : ""} /> Refresh
                </button>
              </div>
            }
          />
        </div>
        <div className="admin-card-body">
          <div className="admin-stat-grid mb-6">
            <StatCard title="Sessions" value={sessions.length} description="Cached context mappings" tone="primary" />
            <StatCard title="Messages" value={totalMessages} description="Cached message snapshots" tone="success" />
            <StatCard title="Tool chats" value={sessionsWithTools} description="Sessions with tool calls" tone="warning" />
            <StatCard title="Selected" value={selectedChat?.messages?.length || 0} description="Messages in current view" tone="default" />
          </div>
          {error ? (
            <div className="mb-4 rounded-lg bg-[var(--danger-light)] p-3 text-sm font-medium text-[var(--danger)]">{error}</div>
          ) : null}
          <div className="admin-table-wrap">
            <table className="admin-table">
              <thead>
                <tr>
                  <th>Account</th>
                  <th>Model</th>
                  <th>Type</th>
                  <th>Messages</th>
                  <th>Last message</th>
                  <th>Updated</th>
                  <th>Tools</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {sessions.length === 0 ? (
                  <tr><td className="empty" colSpan={8}>{loadingList ? "Loading sessions..." : "No cached sessions yet"}</td></tr>
                ) : null}
                {sessions.map((session) => (
                  <tr key={session.context_hash} className={selectedHash === session.context_hash ? "bg-[var(--surface-hover)]" : ""}>
                    <td>
                      <div className="font-medium">{session.account_email || "-"}</div>
                      <div className="mono text-[var(--text-muted)]">{truncate(session.chat_id, 24)}</div>
                    </td>
                    <td className="mono">{session.model || "-"}</td>
                    <td><span className="admin-tag primary">{session.chat_type || "t2t"}</span></td>
                    <td>{session.message_count || 0}</td>
                    <td>{truncate(session.last_message, 90)}</td>
                    <td>{formatSessionTime(session.updated_time)}</td>
                    <td>
                      <div className="flex flex-wrap gap-1">
                        {(session.tools_used || []).length ? session.tools_used?.map((tool) => (
                          <span key={tool} className="admin-tag warning">{tool}</span>
                        )) : <span className="text-[var(--text-muted)]">-</span>}
                      </div>
                    </td>
                    <td>
                      <button className="admin-btn admin-btn-sm admin-btn-secondary" onClick={() => void loadChat(session)}>
                        <Eye size={14} /> View
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <div>
            <h3><MessageSquareText size={16} className="mr-1 inline" />Chat viewer</h3>
            <p>{selectedSession ? `${selectedSession.account_email} · ${selectedSession.model} · ${selectedSession.chat_id}` : "Select a session to inspect cached messages."}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            {selectedChat?.web_interface_url ? (
              <a className="admin-btn admin-btn-secondary" href={selectedChat.web_interface_url} target="_blank" rel="noreferrer">
                <ExternalLink size={16} /> Open in Qwen
              </a>
            ) : null}
            <button className="admin-btn admin-btn-danger" disabled={!selectedSession} onClick={() => void deleteSelectedSession()}>
              <Trash2 size={16} /> Delete session
            </button>
          </div>
        </div>
        <div className="admin-card-body">
          {loadingChat ? <div className="text-sm text-[var(--text-secondary)]">Loading chat cache...</div> : null}
          {selectedChat?.note ? (
            <div className="mb-4 rounded-lg bg-[var(--warning-light)] p-3 text-sm font-medium text-[var(--warning)]">{selectedChat.note}</div>
          ) : null}
          {!selectedChat && !loadingChat ? (
            <div className="rounded-xl border border-dashed border-[var(--border)] p-8 text-center text-sm text-[var(--text-secondary)]">
              Choose a cached session from the table above.
            </div>
          ) : null}
          {selectedChat ? (
            <div className="flex flex-col gap-4">
              {selectedChat.messages.length === 0 ? (
                <div className="rounded-xl border border-dashed border-[var(--border)] p-8 text-center text-sm text-[var(--text-secondary)]">
                  No cached messages for this session yet.
                </div>
              ) : null}
              {selectedChat.messages.map((message, index) => (
                <MessageBubble key={message.id || `${message.role}-${index}`} message={message} />
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

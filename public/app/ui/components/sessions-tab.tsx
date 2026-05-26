"use client";

import { useTranslation } from "react-i18next";
import { useMemo, useState, useCallback } from "react";
import { RefreshCw, Trash2, MessageSquare, ExternalLink, Clock, User, Hash } from "lucide-react";
import { apiRequest } from "../api";

export type SessionItem = {
  context_hash: string;
  account_email: string;
  chat_id: string;
  model: string;
  chat_type: string;
  updated_at: number;
  updated_time: string;
};

export type SessionsListResponse = {
  total: number;
  sessions: SessionItem[];
};

export type ChatSessionResponse = {
  session: SessionItem;
  messages: Array<{
    role: string;
    content: string;
    timestamp?: number;
  }>;
  note?: string;
  web_interface_url?: string;
};

export function SessionsTab({ apiKey }: { apiKey: string }) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [sessions, setSessions] = useState<SessionItem[]>([]);
  const [selectedChatId, setSelectedChatId] = useState<string | null>(null);
  const [chatDetail, setChatDetail] = useState<ChatSessionResponse | null>(null);
  const [loadingChat, setLoadingChat] = useState(false);

  async function loadSessions() {
    try {
      setLoading(true);
      setError("");
      const response = await apiRequest<SessionsListResponse>("/api/sessions", {}, apiKey);
      setSessions(response.sessions || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.failed"));
    } finally {
      setLoading(false);
    }
  }

  async function loadChatDetail(chatId: string) {
    try {
      setLoadingChat(true);
      setChatDetail(null);
      const response = await apiRequest<ChatSessionResponse>(
        `/api/sessions/chat?chat_id=${encodeURIComponent(chatId)}`,
        {},
        apiKey
      );
      setChatDetail(response);
      setSelectedChatId(chatId);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.failed"));
    } finally {
      setLoadingChat(false);
    }
  }

  async function deleteSession(contextHash: string) {
    if (!confirm(t("sessions.confirmDelete"))) return;
    try {
      setError("");
      await apiRequest(
        "/api/sessions",
        {
          method: "DELETE",
          body: JSON.stringify({ context_hash: contextHash }),
        },
        apiKey
      );
      // Reload sessions after delete
      await loadSessions();
      // Clear selected chat if it was deleted
      if (selectedChatId && chatDetail?.session.context_hash === contextHash) {
        setSelectedChatId(null);
        setChatDetail(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.failed"));
    }
  }

  // Load sessions on mount
  useMemo(() => {
    void loadSessions();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const formatTime = useCallback((ts: number) => {
    return new Date(ts).toLocaleString();
  }, []);

  return (
    <div className="admin-grid-2">
      {/* Left panel: Sessions list */}
      <div className="admin-card">
        <div className="admin-card-header">
          <div>
            <h3><MessageSquare size={16} className="inline mr-1" />{t("nav.sessions")}</h3>
            <p>{t("sessions.subtitle")}</p>
          </div>
          <button
            className="admin-btn admin-btn-ghost"
            disabled={loading}
            onClick={() => void loadSessions()}
          >
            <RefreshCw size={16} className={loading ? "animate-spin" : ""} />
            {t("common.refresh")}
          </button>
        </div>
        <div className="admin-card-body">
          {error ? (
            <div className="rounded-lg bg-[var(--danger-light)] p-3 text-sm font-medium text-[var(--danger)] mb-4">
              {error}
            </div>
          ) : null}

          {loading && sessions.length === 0 ? (
            <div className="text-center py-8 text-[var(--text-secondary)]">{t("common.loading")}</div>
          ) : sessions.length === 0 ? (
            <div className="text-center py-8 text-[var(--text-secondary)]">{t("sessions.noSessions")}</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="admin-table">
                <thead>
                  <tr>
                    <th>{t("sessions.chatId")}</th>
                    <th>{t("sessions.model")}</th>
                    <th>{t("sessions.account")}</th>
                    <th>{t("sessions.updatedAt")}</th>
                    <th>{t("common.actions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {sessions.map((session) => (
                    <tr key={session.chat_id}>
                      <td>
                        <div className="flex items-center gap-2">
                          <Hash size={14} className="text-[var(--text-muted)]" />
                          <code className="text-xs">{session.chat_id.slice(0, 12)}...</code>
                        </div>
                      </td>
                      <td>
                        <span className="admin-badge">{session.model || "-"}</span>
                      </td>
                      <td>
                        <div className="flex items-center gap-2">
                          <User size={14} className="text-[var(--text-muted)]" />
                          <span className="text-sm">{session.account_email || "-"}</span>
                        </div>
                      </td>
                      <td>
                        <div className="flex items-center gap-2">
                          <Clock size={14} className="text-[var(--text-muted)]" />
                          <span className="text-sm">{formatTime(session.updated_at)}</span>
                        </div>
                      </td>
                      <td>
                        <div className="flex gap-2">
                          <button
                            className="admin-btn admin-btn-sm admin-btn-primary"
                            onClick={() => void loadChatDetail(session.chat_id)}
                          >
                            <MessageSquare size={14} />
                            {t("sessions.viewChat")}
                          </button>
                          <button
                            className="admin-btn admin-btn-sm admin-btn-danger"
                            onClick={() => void deleteSession(session.context_hash)}
                            title={t("sessions.deleteSession")}
                          >
                            <Trash2 size={14} />
                          </button>
                          <a
                            href={`https://chat.qwen.ai/c/${session.chat_id}`}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="admin-btn admin-btn-sm admin-btn-ghost"
                            title={t("sessions.openInQwen")}
                          >
                            <ExternalLink size={14} />
                          </a>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {/* Right panel: Chat detail */}
      <div className="admin-card">
        <div className="admin-card-header">
          <div>
            <h3><MessageSquare size={16} className="inline mr-1" />{t("sessions.chatDetail")}</h3>
            <p>{t("sessions.chatDetailSubtitle")}</p>
          </div>
        </div>
        <div className="admin-card-body">
          {loadingChat ? (
            <div className="text-center py-8 text-[var(--text-secondary)]">{t("common.loading")}</div>
          ) : !chatDetail ? (
            <div className="text-center py-8 text-[var(--text-secondary)]">
              {t("sessions.selectToView")}
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              {/* Session info */}
              <div className="rounded-lg border border-[var(--border)] bg-[var(--bg)] p-4">
                <h4 className="font-semibold mb-3 text-sm text-[var(--text-secondary)]">
                  {t("sessions.sessionInfo")}
                </h4>
                <div className="grid grid-cols-2 gap-3 text-sm">
                  <div>
                    <span className="text-[var(--text-muted)]">{t("sessions.chatId")}: </span>
                    <code className="ml-1">{chatDetail.session.chat_id}</code>
                  </div>
                  <div>
                    <span className="text-[var(--text-muted)]">{t("sessions.model")}: </span>
                    <span className="ml-1">{chatDetail.session.model || "-"}</span>
                  </div>
                  <div>
                    <span className="text-[var(--text-muted)]">{t("sessions.account")}: </span>
                    <span className="ml-1">{chatDetail.session.account_email || "-"}</span>
                  </div>
                  <div>
                    <span className="text-[var(--text-muted)]">{t("sessions.chatType")}: </span>
                    <span className="ml-1">{chatDetail.session.chat_type || "-"}</span>
                  </div>
                  <div>
                    <span className="text-[var(--text-muted)]">{t("sessions.updatedAt")}: </span>
                    <span className="ml-1">{formatTime(chatDetail.session.updated_at)}</span>
                  </div>
                </div>
              </div>

              {/* Messages */}
              <div>
                <h4 className="font-semibold mb-3 text-sm text-[var(--text-secondary)]">
                  {t("sessions.messages")}
                </h4>
                {chatDetail.messages && chatDetail.messages.length > 0 ? (
                  <div className="flex flex-col gap-3">
                    {chatDetail.messages.map((msg, idx) => (
                      <div
                        key={idx}
                        className={`rounded-lg p-3 ${
                          msg.role === "user"
                            ? "bg-[var(--primary-light)] border border-[var(--primary)]"
                            : msg.role === "system"
                            ? "bg-[var(--warning-light)] border border-[var(--warning)]"
                            : "bg-[var(--bg-secondary)] border border-[var(--border)]"
                        }`}
                      >
                        <div className="flex items-center justify-between mb-2">
                          <span className="text-xs font-semibold uppercase tracking-wide">
                            {msg.role}
                          </span>
                          {msg.timestamp ? (
                            <span className="text-xs text-[var(--text-muted)]">
                              {new Date(msg.timestamp).toLocaleString()}
                            </span>
                          ) : null}
                        </div>
                        <div className="text-sm whitespace-pre-wrap break-words">
                          {msg.content}
                        </div>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="rounded-lg border border-[var(--border)] bg-[var(--bg)] p-4 text-center text-sm text-[var(--text-secondary)]">
                    {chatDetail.note || t("sessions.noMessages")}
                    {chatDetail.web_interface_url ? (
                      <div className="mt-2">
                        <a
                          href={chatDetail.web_interface_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-[var(--primary)] hover:underline inline-flex items-center gap-1"
                        >
                          <ExternalLink size={14} />
                          {t("sessions.openInQwen")}
                        </a>
                      </div>
                    ) : null}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

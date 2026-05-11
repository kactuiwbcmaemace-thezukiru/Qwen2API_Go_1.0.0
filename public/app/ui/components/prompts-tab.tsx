"use client";

import { AlertTriangle, RotateCcw, Save } from "lucide-react";
import { useMemo, useState } from "react";
import { normalizePromptsResponse } from "../prompts";
import type { PromptItem, PromptsResponse } from "../types";

export function PromptsTab({
  prompts,
  savingSettings,
  savePrompts,
  resetPrompts,
}: {
  prompts: PromptsResponse | null;
  savingSettings: boolean;
  savePrompts: (updates: Record<string, string>) => Promise<void>;
  resetPrompts: (ids: string[]) => Promise<void>;
}) {
  const [category, setCategory] = useState("all");
  const [risk, setRisk] = useState<"all" | "protocol" | "normal">("all");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const normalizedPrompts = useMemo(() => normalizePromptsResponse(prompts), [prompts]);

  const items = useMemo(() => normalizedPrompts?.data || [], [normalizedPrompts]);
  const filtered = useMemo(
    () =>
      items.filter((item) => {
        const categoryMatched = category === "all" || item.category === category;
        const riskMatched =
          risk === "all" ||
          (risk === "protocol" && item.risk === "protocol") ||
          (risk === "normal" && item.risk !== "protocol");
        return categoryMatched && riskMatched;
      }),
    [category, items, risk],
  );

  const changedIds = Object.keys(drafts);
  const modifiedCount = items.filter((item) => item.modified).length;

  function updateDraft(item: PromptItem, value: string) {
    setDrafts((current) => {
      const next = { ...current };
      if (value === item.value) {
        delete next[item.id];
      } else {
        next[item.id] = value;
      }
      return next;
    });
  }

  async function saveOne(item: PromptItem) {
    await savePrompts({ [item.id]: drafts[item.id] ?? item.value });
    setDrafts((current) => {
      const next = { ...current };
      delete next[item.id];
      return next;
    });
  }

  async function saveAllChanged() {
    const updates = Object.fromEntries(changedIds.map((id) => [id, drafts[id] ?? ""]));
    await savePrompts(updates);
    setDrafts({});
  }

  async function reset(ids: string[]) {
    await resetPrompts(ids);
    setDrafts((current) => {
      if (!ids.length) {
        return {};
      }
      const next = { ...current };
      ids.forEach((id) => delete next[id]);
      return next;
    });
  }

  if (!normalizedPrompts) {
    return (
      <div className="asset-empty-state">
        <strong>提示词配置未加载</strong>
        <span>请确认后端已更新并重新加载控制台。</span>
      </div>
    );
  }

  return (
    <div className="prompts-layout">
      <aside className="prompts-sidebar">
        <div className="admin-card">
          <div className="admin-card-header">
            <div>
              <h3>提示词</h3>
              <p>统一管理内置提示词与协议模板。</p>
            </div>
          </div>
          <div className="admin-card-body flex flex-col gap-4">
            <div className="prompt-mini-stats">
              <div>
                <strong>{items.length}</strong>
                <span>全部</span>
              </div>
              <div>
                <strong>{modifiedCount}</strong>
                <span>已修改</span>
              </div>
              <div>
                <strong>{changedIds.length}</strong>
                <span>未保存</span>
              </div>
            </div>

            <div className="admin-form-group">
              <label>分类</label>
              <select className="admin-select" value={category} onChange={(event) => setCategory(event.target.value)}>
                <option value="all">全部分类</option>
                {normalizedPrompts.categories.map((item) => (
                  <option key={item} value={item}>
                    {item}
                  </option>
                ))}
              </select>
            </div>

            <div className="admin-form-group">
              <label>风险</label>
              <select className="admin-select" value={risk} onChange={(event) => setRisk(event.target.value as typeof risk)}>
                <option value="all">全部</option>
                <option value="normal">普通</option>
                <option value="protocol">高风险协议</option>
              </select>
            </div>

            <button className="admin-btn admin-btn-primary" disabled={!changedIds.length || savingSettings} onClick={() => void saveAllChanged()}>
              <Save size={16} />
              保存全部未保存
            </button>
            <button className="admin-btn admin-btn-danger" disabled={savingSettings || !items.length} onClick={() => void reset([])}>
              <RotateCcw size={16} />
              全部恢复默认
            </button>
          </div>
        </div>
      </aside>

      <section className="prompts-list">
        {filtered.map((item) => {
          const draft = drafts[item.id] ?? item.value;
          const changed = draft !== item.value;
          return (
            <article className="admin-card prompt-editor" key={item.id}>
              <div className="admin-card-header">
                <div>
                  <div className="prompt-title-row">
                    <h3>{item.title}</h3>
                    {item.risk === "protocol" ? (
                      <span className="prompt-badge danger">
                        <AlertTriangle size={14} />
                        高风险协议
                      </span>
                    ) : null}
                    <span className={`prompt-badge ${item.modified ? "changed" : ""}`}>
                      {item.modified ? "已修改" : "内置默认"}
                    </span>
                    {changed ? <span className="prompt-badge unsaved">未保存</span> : null}
                  </div>
                  <p>{item.description}</p>
                </div>
              </div>
              <div className="admin-card-body flex flex-col gap-4">
                <div className="prompt-meta">
                  <span>{item.category}</span>
                  <code>{item.id}</code>
                </div>
                {item.placeholders.length ? (
                  <div className="prompt-placeholders">
                    {item.placeholders.map((placeholder) => (
                      <code key={placeholder}>{placeholder}</code>
                    ))}
                  </div>
                ) : null}
                <textarea
                  className="admin-textarea prompt-textarea"
                  value={draft}
                  rows={Math.min(18, Math.max(6, draft.split("\n").length + 2))}
                  onChange={(event) => updateDraft(item, event.target.value)}
                />
                <div className="flex flex-wrap gap-3">
                  <button className="admin-btn admin-btn-primary" disabled={!changed || savingSettings} onClick={() => void saveOne(item)}>
                    <Save size={16} />
                    保存
                  </button>
                  <button
                    className="admin-btn admin-btn-secondary"
                    disabled={savingSettings}
                    onClick={() => updateDraft(item, item.defaultValue)}
                  >
                    使用内置值
                  </button>
                  <button className="admin-btn admin-btn-ghost" disabled={savingSettings} onClick={() => void reset([item.id])}>
                    <RotateCcw size={16} />
                    恢复默认
                  </button>
                </div>
              </div>
            </article>
          );
        })}
        {!filtered.length ? <div className="asset-alert">没有匹配的提示词。</div> : null}
      </section>
    </div>
  );
}

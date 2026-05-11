"use client";

import {
  Copy,
  ExternalLink,
  ImageIcon,
  RefreshCw,
  Sparkles,
  Video,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { ApiRequestError, apiRequest, apiRequestEnvelope } from "../api";
import type { ModelItem, ModelsResponse } from "../types";

type AssetKind = "image" | "video";

type AssetGenerationResponse = {
  id?: string;
  object?: string;
  created?: number;
  status?: string;
  data?: Array<{
    url?: string;
    b64_json?: string;
  }>;
};

type AssetGenerationConfig = {
  kind: AssetKind;
  title: string;
  description: string;
  apiPath: string;
  modelSuffix: string;
  promptPlaceholder: string;
  defaultPrompt: string;
  submitLabel: string;
  loadingLabel: string;
};

const IMAGE_SIZE_OPTIONS = [
  { value: "1024x1024", label: "1:1  正方形" },
  { value: "1536x1024", label: "4:3  横图" },
  { value: "1024x1536", label: "3:4  竖图" },
  { value: "1792x1024", label: "16:9 宽屏" },
  { value: "1024x1792", label: "9:16 竖屏" },
];

const VIDEO_SIZE_OPTIONS = [
  { value: "1:1", label: "1:1  正方形" },
  { value: "3:4", label: "3:4  竖图" },
  { value: "4:3", label: "4:3  横图" },
  { value: "16:9", label: "16:9 宽屏" },
  { value: "9:16", label: "9:16 竖屏" },
];

const CONFIGS: Record<AssetKind, AssetGenerationConfig> = {
  image: {
    kind: "image",
    title: "AI 生图",
    description: "调用 /v1/images/generations，用当前后台登录 Key 生成图片。",
    apiPath: "/v1/images/generations",
    modelSuffix: "-image",
    promptPlaceholder: "描述你想生成的画面、主体、风格、构图和细节。",
    defaultPrompt: "一张干净的产品海报，玻璃质感的 Qwen2API 标志放在桌面中央，柔和棚拍光，高清细节",
    submitLabel: "生成图片",
    loadingLabel: "生成中...",
  },
  video: {
    kind: "video",
    title: "AI 生视频",
    description: "调用 /v1/videos，用当前后台登录 Key 生成视频。",
    apiPath: "/v1/videos",
    modelSuffix: "-video",
    promptPlaceholder: "描述视频画面、动作、镜头、节奏和风格。",
    defaultPrompt: "一个发光的 Qwen2API 标志从深色工作台上缓慢升起，镜头轻微推进，科技感，流畅运动",
    submitLabel: "生成视频",
    loadingLabel: "生成中...",
  },
};

export function AssetGenerationTab({ kind, apiKey, defaultPrompt }: { kind: AssetKind; apiKey: string; defaultPrompt?: string }) {
  const config = CONFIGS[kind];
  const configuredDefaultPrompt = defaultPrompt || config.defaultPrompt;
  const Icon = kind === "image" ? ImageIcon : Video;
  const sizeOptions = kind === "video" ? VIDEO_SIZE_OPTIONS : IMAGE_SIZE_OPTIONS;
  const [models, setModels] = useState<ModelItem[]>([]);
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState(configuredDefaultPrompt);
  const [size, setSize] = useState(sizeOptions[0].value);
  const [loading, setLoading] = useState(false);
  const [loadingModels, setLoadingModels] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<AssetGenerationResponse | null>(null);
  const [raw, setRaw] = useState("");
  const [copied, setCopied] = useState(false);

  const selectedModel = model || models[0]?.id || "";
  const selectedSize = sizeOptions.some((item) => item.value === size) ? size : sizeOptions[0].value;
  const resultUrl = result?.data?.[0]?.url || "";
  const canSubmit = Boolean(selectedModel) && Boolean(prompt.trim()) && !loading;

  const curlExample = useMemo(
    () => `curl -X POST ${config.apiPath} \\
  -H "Authorization: Bearer ${apiKey ? "***已登录***" : "sk-admin"}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model":"${selectedModel || config.modelSuffix.replace("-", "qwen-")}",
    "prompt":"${prompt.trim() || "描述要生成的内容"}",
    "size":"${selectedSize}"
  }'`,
    [apiKey, config.apiPath, config.modelSuffix, prompt, selectedModel, selectedSize],
  );

  useEffect(() => {
    let cancelled = false;

    async function loadModels() {
      if (!apiKey) {
        return;
      }
      try {
        setLoadingModels(true);
        const response = await apiRequest<ModelsResponse>("/api/models", {}, apiKey);
        const filtered = (response.data || []).filter((item) => item.id.endsWith(config.modelSuffix));
        if (cancelled) {
          return;
        }
        setModels(filtered);
        setModel((current) => {
          if (current && filtered.some((item) => item.id === current)) {
            return current;
          }
          return filtered[0]?.id || "";
        });
      } catch {
        if (!cancelled) {
          setError("加载模型列表失败。");
        }
      } finally {
        if (!cancelled) {
          setLoadingModels(false);
        }
      }
    }

    void loadModels();

    return () => {
      cancelled = true;
    };
  }, [apiKey, config.modelSuffix]);

  async function submitGeneration() {
    if (!canSubmit) {
      return;
    }

    try {
      setLoading(true);
      setError("");
      setCopied(false);
      setResult(null);
      setRaw("");

      const response = await apiRequestEnvelope<AssetGenerationResponse>(
        config.apiPath,
        {
          method: "POST",
          body: JSON.stringify({
            model: selectedModel,
            prompt: prompt.trim(),
            size: selectedSize,
          }),
        },
        apiKey,
      );
      setResult(response.body);
      setRaw(JSON.stringify(response, null, 2));

      if (!response.body.data?.[0]?.url) {
        setError("未解析到资源链接。");
      }
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setRaw(JSON.stringify(err.response, null, 2));
      }
      setError(err instanceof Error ? err.message : "生成请求失败。");
    } finally {
      setLoading(false);
    }
  }

  async function copyResultUrl() {
    if (!resultUrl) {
      return;
    }
    try {
      await navigator.clipboard.writeText(resultUrl);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setError("复制失败，请手动复制链接。");
    }
  }

  if (loadingModels) {
    return (
      <div className="asset-empty-state">
        <RefreshCw size={22} className="animate-spin" />
        <strong>正在加载模型</strong>
        <span>请稍候...</span>
      </div>
    );
  }

  return (
    <div className="asset-tool-grid">
      <section className="admin-card">
        <div className="admin-card-header">
          <div>
            <h3>生成参数</h3>
            <p>选择能力模型，填写提示词后直接调用后端兼容接口。</p>
          </div>
        </div>
        <div className="admin-card-body flex flex-col gap-5">
          <div className="admin-form-grid">
            <div className="admin-form-group">
              <label>{kind === "image" ? "生图模型" : "生视频模型"}</label>
              <select className="admin-select" value={selectedModel} onChange={(event) => setModel(event.target.value)}>
                {models.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.id}
                  </option>
                ))}
              </select>
            </div>
            <div className="admin-form-group">
              <label>尺寸比例</label>
              <select className="admin-select" value={selectedSize} onChange={(event) => setSize(event.target.value)}>
                {sizeOptions.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="admin-form-group">
              <label>可用模型</label>
              <div className="asset-inline-stat">
                <strong>{models.length}</strong>
                <span>{config.modelSuffix}</span>
              </div>
            </div>
          </div>

          <div className="admin-form-group">
            <label>Prompt</label>
            <textarea
              className="admin-textarea"
              rows={8}
              placeholder={config.promptPlaceholder}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
            />
          </div>

          <div className="flex flex-wrap gap-3">
            <button className="admin-btn admin-btn-primary" disabled={!canSubmit} onClick={() => void submitGeneration()}>
              {loading ? <RefreshCw size={16} className="animate-spin" /> : <Sparkles size={16} />}
              {loading ? config.loadingLabel : config.submitLabel}
            </button>
            <button
              className="admin-btn admin-btn-secondary"
              disabled={loading}
              onClick={() => {
                setPrompt(configuredDefaultPrompt);
                setResult(null);
                setRaw("");
                setError("");
                setCopied(false);
              }}
            >
              重置
            </button>
          </div>

          {!models.length ? (
            <div className="asset-alert">
              当前模型列表里没有 {config.modelSuffix} 变体，请先确认账号或模型能力。
            </div>
          ) : null}
          {error ? <div className="asset-alert danger">{error}</div> : null}
        </div>
      </section>

      <section className="admin-card">
        <div className="admin-card-header">
          <div>
            <h3>生成结果</h3>
            <p>成功后会展示资源预览、链接和原始响应。</p>
          </div>
        </div>
        <div className="admin-card-body flex flex-col gap-5">
          <div className="asset-preview">
            {resultUrl && kind === "image" ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={resultUrl} alt="AI 生成图片" />
            ) : null}
            {resultUrl && kind === "video" ? (
              <video src={resultUrl} controls playsInline />
            ) : null}
            {!resultUrl ? (
              <div className="asset-preview-empty">
                <Icon size={36} />
                <strong>暂无生成结果</strong>
                <span>提交请求后，资源会显示在这里。</span>
              </div>
            ) : null}
          </div>

          {resultUrl ? (
            <div className="asset-result-actions">
              <a className="admin-btn admin-btn-secondary" href={resultUrl} target="_blank" rel="noreferrer">
                <ExternalLink size={16} />
                打开链接
              </a>
              <button className="admin-btn admin-btn-ghost" onClick={() => void copyResultUrl()}>
                <Copy size={16} />
                {copied ? "已复制" : "复制 URL"}
              </button>
            </div>
          ) : null}

          <div className="admin-form-group">
            <label>资源 URL</label>
            <div className="asset-url-box">{resultUrl || "生成成功后会显示资源链接。"}</div>
          </div>

          <div className="admin-form-group">
            <label>请求示例</label>
            <pre className="admin-code">{curlExample}</pre>
          </div>

          <div className="admin-form-group">
            <label>完整响应 JSON</label>
            <pre className="admin-code">{raw || "{ }"}</pre>
          </div>
        </div>
      </section>
    </div>
  );
}

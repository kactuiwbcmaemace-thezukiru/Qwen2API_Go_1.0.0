import type { PromptItem, PromptsResponse } from "./types";

export const PROMPT_IDS = {
  debugSystem: "frontend.debug.system",
  imageDefault: "frontend.image.default",
  videoDefault: "frontend.video.default",
} as const;

const FALLBACKS: Record<string, string> = {
  [PROMPT_IDS.debugSystem]: "你是一个用于后台调试的助手，请直接、简洁地回答。",
  [PROMPT_IDS.imageDefault]: "一张干净的产品海报，玻璃质感的 Qwen2API 标志放在桌面中央，柔和棚拍光，高清细节",
  [PROMPT_IDS.videoDefault]: "一个发光的 Qwen2API 标志从深色工作台上缓慢升起，镜头轻微推进，科技感，流畅运动",
};

export function promptValue(prompts: PromptsResponse | null | undefined, id: string) {
  return normalizePromptsResponse(prompts)?.data.find((item) => item.id === id)?.value ?? FALLBACKS[id] ?? "";
}

export function normalizePromptsResponse(value: unknown): PromptsResponse | null {
  if (!value || typeof value !== "object") {
    return null;
  }

  const payload = value as Partial<PromptsResponse>;
  if (!Array.isArray(payload.data)) {
    return null;
  }

  const rawItems: unknown[] = Array.isArray(payload.data) ? payload.data : [];
  const data = rawItems
    .filter((item): item is Partial<PromptItem> => Boolean(item) && typeof item === "object")
    .map((item) => ({
      id: typeof item.id === "string" ? item.id : "",
      category: typeof item.category === "string" ? item.category : "未分类",
      title: typeof item.title === "string" ? item.title : "未命名提示词",
      description: typeof item.description === "string" ? item.description : "",
      defaultValue: typeof item.defaultValue === "string" ? item.defaultValue : "",
      value: typeof item.value === "string" ? item.value : "",
      risk: typeof item.risk === "string" ? item.risk : "",
      placeholders: Array.isArray(item.placeholders)
        ? item.placeholders.filter((placeholder): placeholder is string => typeof placeholder === "string")
        : [],
      modified: Boolean(item.modified),
    }))
    .filter((item) => item.id);

  const categories = Array.isArray(payload.categories)
    ? payload.categories.filter((item): item is string => typeof item === "string")
    : Array.from(new Set(data.map((item) => item.category)));

  return { data, categories };
}

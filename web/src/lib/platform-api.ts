import type { ModelChannel } from "@/stores/use-config-store";

/** 平台开放接口的站内前缀；Claude 客户端会自行补 /v1/messages，OpenAI 客户端填到 /v1。 */
export const PLATFORM_API_PATH = "/api/open";

export type PlatformAPIEndpoint = "messages" | "chat" | "tasks";

export interface PlatformAPIModel {
    model: string;
    displayName: string;
    endpoint: PlatformAPIEndpoint;
    /** 每百万输出 Token 的微积分单价，用于给 Claude Code 后台小任务挑便宜的模型；按次计费的模型为 0。 */
    outputTokenPriceMicrocredits: number;
}

/**
 * 外部工具填写的接口根地址。apiPath 是已按 VITE_CANVAS_BACKEND_URL 映射后的开放接口路径：
 * 前后端分开部署时它已是完整地址，否则补上当前站点域名。
 */
export function platformAPIBaseURL(origin: string, apiPath: string = PLATFORM_API_PATH) {
    const path = apiPath.replace(/\/+$/u, "");
    return /^https?:\/\//iu.test(path) ? path : `${origin.replace(/\/+$/u, "")}${path}`;
}

export function platformAPIOpenAIBaseURL(origin: string, apiPath: string = PLATFORM_API_PATH) {
    return `${platformAPIBaseURL(origin, apiPath)}/v1`;
}

export function maskedUserAPIKey(key: { keyPrefix: string; keyLast4: string }) {
    return `${key.keyPrefix}…${key.keyLast4}`;
}

/** 与后端开放接口的分流规则一致：Claude 协议走 /messages，OpenAI 协议走 /chat/completions，图片视频走 /tasks。 */
export function platformAPIModels(channels: ModelChannel[]): PlatformAPIModel[] {
    const seen = new Set<string>();
    const models: PlatformAPIModel[] = [];
    for (const channel of channels) {
        if (channel.scope !== "system") continue;
        for (const cost of channel.modelCosts || []) {
            const model = cost.model;
            if (!model || seen.has(model)) continue;
            const endpoint = platformAPIEndpoint(cost.capability, String(cost.protocol || ""));
            if (!endpoint) continue;
            seen.add(model);
            models.push({ model, displayName: cost.displayName || model, endpoint, outputTokenPriceMicrocredits: cost.outputTokenPriceMicrocredits || 0 });
        }
    }
    return models;
}

function platformAPIEndpoint(capability: string | undefined, protocol: string): PlatformAPIEndpoint | null {
    if (capability === "text") {
        if (protocol === "claude-api") return "messages";
        if (protocol === "chat-completion") return "chat";
        return null;
    }
    return capability === "image" || capability === "video" ? "tasks" : null;
}

export function claudeCodeSetupScript(baseURL: string, secret: string, models: PlatformAPIModel[]) {
    const claudeModels = models.filter((item) => item.endpoint === "messages");
    const main = claudeModels[0]?.model || "<模型名>";
    // Claude Code 的后台小任务默认找 haiku；站上没有时指到输出单价最低的 Claude 格式模型，避免按主力模型的价格扣费。
    const background = [...claudeModels].sort((left, right) => left.outputTokenPriceMicrocredits - right.outputTokenPriceMicrocredits)[0]?.model || main;
    return [`export ANTHROPIC_BASE_URL=${baseURL}`, `export ANTHROPIC_AUTH_TOKEN=${secret}`, `export ANTHROPIC_MODEL=${main}`, `export ANTHROPIC_DEFAULT_HAIKU_MODEL=${background}`, "claude"].join("\n");
}

import { expect, test } from "bun:test";

import { claudeCodeSetupScript, maskedUserAPIKey, platformAPIBaseURL, platformAPIModels, platformAPIOpenAIBaseURL } from "../src/lib/platform-api";
import type { ModelChannel } from "../src/stores/use-config-store";

const origin = "https://example.com/";

function systemChannel(id: string, costs: Array<{ model: string; capability: "text" | "image" | "video" | "audio"; protocol?: string; displayName?: string; outputTokenPriceMicrocredits?: number }>, scope: "system" | "user" = "system"): ModelChannel {
    return {
        id,
        name: id,
        baseUrl: `/api/${id}`,
        apiKey: "system",
        scope,
        models: costs.map((item) => item.model),
        modelCosts: costs.map((item) => ({ ...item, billingMode: "fixed_request", unitPriceMicrocredits: 0 })),
    } as unknown as ModelChannel;
}

test("接入地址按站点域名生成，Claude 客户端不带 /v1，OpenAI 客户端带 /v1", () => {
    expect(platformAPIBaseURL(origin)).toBe("https://example.com/api/open");
    expect(platformAPIOpenAIBaseURL(origin)).toBe("https://example.com/api/open/v1");
});

test("前后端分开部署时使用后端地址", () => {
    expect(platformAPIBaseURL(origin, "https://backend.example.com/open/")).toBe("https://backend.example.com/open");
    expect(platformAPIOpenAIBaseURL(origin, "https://backend.example.com/open")).toBe("https://backend.example.com/open/v1");
});

test("Key 列表只显示前后几位", () => {
    expect(maskedUserAPIKey({ keyPrefix: "sk-ab12", keyLast4: "cd34" })).toBe("sk-ab12…cd34");
});

test("可用模型按协议分到对应接口，同名只列一次，个人渠道和不支持的协议不列出", () => {
    const models = platformAPIModels([
        systemChannel("claude", [{ model: "claude-test", capability: "text", protocol: "claude-api", displayName: "Claude", outputTokenPriceMicrocredits: 5 }]),
        systemChannel("deepseek", [
            { model: "deepseek-test", capability: "text", protocol: "chat-completion" },
            { model: "responses-only", capability: "text", protocol: "openai-response" },
        ]),
        systemChannel("media", [
            { model: "image-test", capability: "image", protocol: "openai-image" },
            { model: "video-test", capability: "video", protocol: "minimax-video" },
            { model: "tts-test", capability: "audio", protocol: "openai-audio" },
        ]),
        systemChannel("duplicate", [{ model: "claude-test", capability: "text", protocol: "claude-api" }]),
        systemChannel("personal", [{ model: "my-model", capability: "text", protocol: "chat-completion" }], "user"),
    ]);
    expect(models).toEqual([
        { model: "claude-test", displayName: "Claude", endpoint: "messages", outputTokenPriceMicrocredits: 5 },
        { model: "deepseek-test", displayName: "deepseek-test", endpoint: "chat", outputTokenPriceMicrocredits: 0 },
        { model: "image-test", displayName: "image-test", endpoint: "tasks", outputTokenPriceMicrocredits: 0 },
        { model: "video-test", displayName: "video-test", endpoint: "tasks", outputTokenPriceMicrocredits: 0 },
    ]);
});

test("Claude Code 配置：主力用第一个 Claude 格式模型，后台小任务用输出最便宜的那个", () => {
    const script = claudeCodeSetupScript("https://example.com/api/open", "sk-secret", [
        { model: "deepseek-test", displayName: "DeepSeek", endpoint: "chat", outputTokenPriceMicrocredits: 1 },
        { model: "claude-opus", displayName: "Opus", endpoint: "messages", outputTokenPriceMicrocredits: 9000 },
        { model: "claude-sonnet", displayName: "Sonnet", endpoint: "messages", outputTokenPriceMicrocredits: 3000 },
    ]);
    expect(script).toContain("export ANTHROPIC_BASE_URL=https://example.com/api/open\n");
    expect(script).toContain("export ANTHROPIC_AUTH_TOKEN=sk-secret\n");
    expect(script).toContain("export ANTHROPIC_MODEL=claude-opus\n");
    expect(script).toContain("export ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-sonnet\n");
    expect(claudeCodeSetupScript("https://example.com/api/open", "sk-secret", [])).toContain("ANTHROPIC_MODEL=<模型名>");
});

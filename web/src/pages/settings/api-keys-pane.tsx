import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input } from "antd";
import { Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";

import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { IconButton } from "@/components/ui/base/buttons";
import { AppModal } from "@/components/ui/product/app-modal";
import { Callout } from "@/components/ui/product/callout";
import { RelativeTime } from "@/components/ui/product/relative-time";
import { claudeCodeSetupScript, maskedUserAPIKey, PLATFORM_API_PATH, platformAPIBaseURL, platformAPIModels, platformAPIOpenAIBaseURL, type PlatformAPIEndpoint } from "@/lib/platform-api";
import { createUserAPIKey, deleteUserAPIKey, listUserAPIKeys, type CreatedUserAPIKey, type UserAPIKey } from "@/services/api/api-keys";
import { resolveBackendApiUrl, useEffectiveConfig } from "@/stores/use-config-store";

const NAME_MAX_LENGTH = 40;
const API_KEYS_QUERY_KEY = ["api-keys"] as const;

const ENDPOINT_LABELS: Record<PlatformAPIEndpoint, string> = {
    messages: "Claude 格式（/v1/messages）",
    chat: "OpenAI 格式（/v1/chat/completions）",
    tasks: "生图、生视频（/v1/tasks）",
};

function errorText(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

export function APIKeysPane() {
    const { message, modal } = App.useApp();
    const queryClient = useQueryClient();
    const config = useEffectiveConfig();
    const origin = typeof window === "undefined" ? "" : window.location.origin;
    const apiPath = resolveBackendApiUrl(PLATFORM_API_PATH);
    const baseURL = platformAPIBaseURL(origin, apiPath);
    const openAIBaseURL = platformAPIOpenAIBaseURL(origin, apiPath);
    const models = useMemo(() => platformAPIModels(config.channels), [config.channels]);
    const keys = useQuery({ queryKey: API_KEYS_QUERY_KEY, queryFn: listUserAPIKeys });
    const [createOpen, setCreateOpen] = useState(false);
    const [name, setName] = useState("");
    const [created, setCreated] = useState<CreatedUserAPIKey | null>(null);

    const create = useMutation({
        mutationFn: (value: string) => createUserAPIKey(value),
        onSuccess: (result) => {
            setCreateOpen(false);
            setName("");
            setCreated(result);
            void queryClient.invalidateQueries({ queryKey: API_KEYS_QUERY_KEY });
        },
        onError: (error) => message.error(errorText(error, "新建 API Key 失败")),
    });
    const remove = useMutation({
        mutationFn: (id: string) => deleteUserAPIKey(id),
        onSuccess: () => {
            message.success("API Key 已删除，使用它的工具会立即失效");
            void queryClient.invalidateQueries({ queryKey: API_KEYS_QUERY_KEY });
        },
        onError: (error) => message.error(errorText(error, "删除 API Key 失败")),
    });

    const copy = async (value: string, label: string) => {
        try {
            await navigator.clipboard.writeText(value);
            message.success(`${label}已复制`);
        } catch {
            message.error("复制失败，请手动选择后复制");
        }
    };
    const confirmRemove = (key: UserAPIKey) => {
        modal.confirm({
            title: `删除「${key.name}」？`,
            content: "删除后立即失效，用这个 Key 的工具都需要换成新的 Key。",
            okText: "删除",
            okButtonProps: { danger: true },
            cancelText: "取消",
            onOk: () => remove.mutateAsync(key.id),
        });
    };

    const list = keys.data?.keys ?? [];
    const limit = keys.data?.limit ?? 0;
    const atLimit = limit > 0 && list.length >= limit;
    const trimmedName = name.trim();

    return (
        <>
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>API Key</h2>
                    <p>在 Claude Code、Cherry Studio 或自己的程序里调用站内模型。扣的是同一个积分余额，价格和网页一样。</p>
                </div>
                <Button type="primary" icon={<Plus className="size-4" />} disabled={atLimit || keys.isPending} onClick={() => setCreateOpen(true)}>
                    新建 Key
                </Button>
            </div>
            <div className="settings-section space-y-5">
                <section className="space-y-2" aria-label="接入地址">
                    <h3 className="text-sm font-medium text-foreground">接入地址</h3>
                    <AddressRow label="Claude 格式（Claude Code 等）" value={baseURL} onCopy={(value) => void copy(value, "地址")} />
                    <AddressRow label="OpenAI 格式（Cherry Studio 等）" value={openAIBaseURL} onCopy={(value) => void copy(value, "地址")} />
                    {models.length ? (
                        <div className="space-y-1 pt-1 text-caption text-muted-foreground">
                            {(Object.keys(ENDPOINT_LABELS) as PlatformAPIEndpoint[]).map((endpoint) => {
                                const names = models.filter((item) => item.endpoint === endpoint).map((item) => item.model);
                                return names.length ? (
                                    <p key={endpoint}>
                                        {ENDPOINT_LABELS[endpoint]}：<span className="font-mono text-foreground/80">{names.join("、")}</span>
                                    </p>
                                ) : null;
                            })}
                        </div>
                    ) : null}
                </section>

                <section className="space-y-2" aria-label="我的 API Key">
                    <div className="flex items-center justify-between gap-2">
                        <h3 className="text-sm font-medium text-foreground">我的 Key</h3>
                        {limit ? (
                            <span className="text-caption text-muted-foreground">
                                {list.length} / {limit}
                            </span>
                        ) : null}
                    </div>
                    {keys.isPending ? (
                        <div className="h-16 animate-pulse rounded-2xl bg-surface-secondary" aria-busy="true" />
                    ) : keys.error ? (
                        <WorkspaceErrorState compact title="读取 API Key 失败" description={errorText(keys.error, "请稍后重试")} onRetry={() => void keys.refetch()} />
                    ) : list.length === 0 ? (
                        <WorkspaceState
                            compact
                            icon="empty"
                            title="还没有 API Key"
                            description="新建一个 Key，填进外部工具就能调用站内模型。"
                            action={
                                <Button type="primary" icon={<Plus className="size-4" />} onClick={() => setCreateOpen(true)}>
                                    新建 Key
                                </Button>
                            }
                        />
                    ) : (
                        <ul className="space-y-2">
                            {list.map((key) => (
                                <li key={key.id} className="flex items-center gap-3 rounded-2xl bg-surface-secondary/80 px-4 py-3">
                                    <KeyRound aria-hidden className="size-4 shrink-0 text-muted-foreground" />
                                    <div className="min-w-0 flex-1">
                                        <p className="truncate text-sm font-medium text-foreground">{key.name}</p>
                                        <p className="mt-0.5 flex flex-wrap gap-x-3 text-caption text-muted-foreground">
                                            <span className="font-mono">{maskedUserAPIKey(key)}</span>
                                            <span>
                                                创建于 <RelativeTime value={key.createdAt} />
                                            </span>
                                            <span>{key.lastUsedAt ? <RelativeTime value={key.lastUsedAt} suffix="使用过" /> : "还没用过"}</span>
                                        </p>
                                    </div>
                                    <IconButton variant="danger" size="sm" icon={Trash2} aria-label={`删除 ${key.name}`} loading={remove.isPending && remove.variables === key.id} onClick={() => confirmRemove(key)} />
                                </li>
                            ))}
                        </ul>
                    )}
                    {atLimit ? <p className="text-caption text-muted-foreground">已达到 {limit} 个上限，删除不用的 Key 后才能新建。</p> : null}
                </section>
            </div>

            <AppModal open={createOpen} title="新建 API Key" okText="新建" okButtonProps={{ disabled: !trimmedName }} confirmLoading={create.isPending} onOk={() => create.mutate(trimmedName)} onCancel={() => setCreateOpen(false)}>
                <div className="space-y-2 pt-2">
                    <p className="text-caption text-muted-foreground">起个名字，方便以后认出是哪台电脑或哪个工具在用。</p>
                    <Input
                        autoFocus
                        value={name}
                        maxLength={NAME_MAX_LENGTH}
                        showCount
                        placeholder="例如：公司电脑的 Claude Code"
                        onChange={(event) => setName(event.target.value)}
                        onPressEnter={() => trimmedName && !create.isPending && create.mutate(trimmedName)}
                    />
                </div>
            </AppModal>

            <AppModal
                open={Boolean(created)}
                title="API Key 已创建"
                width={640}
                okText="我已保存"
                cancelButtonProps={{ style: { display: "none" } }}
                closable={false}
                maskClosable={false}
                onOk={() => {
                    setCreated(null);
                    create.reset();
                }}
            >
                {created ? (
                    <div className="space-y-4 pt-2">
                        <Callout tone="warning" title="完整的 Key 只显示这一次">
                            关掉这个窗口后就看不到了。请现在复制保存；丢了只能删掉重建。
                        </Callout>
                        <div className="flex gap-2">
                            <Input readOnly value={created.secret} aria-label="API Key" className="font-mono" onFocus={(event) => event.currentTarget.select()} />
                            <Button type="primary" icon={<Copy className="size-3.5" />} onClick={() => void copy(created.secret, "API Key")}>
                                复制
                            </Button>
                        </div>
                        <div className="space-y-2">
                            <div className="flex items-center justify-between gap-2">
                                <h3 className="text-sm font-medium text-foreground">Claude Code 配置</h3>
                                <Button size="small" icon={<Copy className="size-3.5" />} onClick={() => void copy(claudeCodeSetupScript(baseURL, created.secret, models), "配置")}>
                                    复制
                                </Button>
                            </div>
                            <pre className="thin-scrollbar overflow-x-auto rounded-xl bg-surface-secondary px-4 py-3 font-mono text-caption leading-6 text-foreground">{claudeCodeSetupScript(baseURL, created.secret, models)}</pre>
                            <p className="text-caption text-muted-foreground">OpenAI 格式的工具：接口地址填 {openAIBaseURL}，API Key 填上面这个。</p>
                        </div>
                    </div>
                ) : null}
            </AppModal>
        </>
    );
}

function AddressRow({ label, value, onCopy }: { label: string; value: string; onCopy: (value: string) => void }) {
    return (
        <div className="flex flex-wrap items-center gap-2">
            <span className="w-52 shrink-0 text-caption text-muted-foreground">{label}</span>
            <code className="min-w-0 flex-1 truncate rounded-lg bg-surface-secondary px-3 py-1.5 font-mono text-caption text-foreground">{value}</code>
            <IconButton variant="ghost" size="sm" icon={Copy} aria-label={`复制${label}地址`} onClick={() => onCopy(value)} />
        </div>
    );
}

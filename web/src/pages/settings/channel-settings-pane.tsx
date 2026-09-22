import { App, Button, Form, Input, Popconfirm, Segmented, Select, Tooltip } from "antd";
import { BookOpen, ChevronDown, Pencil, PlugZap, Plus, RefreshCw, Trash2, Workflow } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

import { ModelEditorModal } from "@/components/model-editor-modal";
import { ChannelHeadersEditor, validateChannelHeaders } from "@/components/channel-headers-editor";
import { WorkspaceState } from "@/components/layout/workspace-state";
import { mergeFetchedChannelModelCosts } from "@/lib/channel-model-catalog";
import { fetchChannelModels } from "@/services/api/image";
import {
    createModelChannel,
    defaultBaseUrlForApiFormat,
    filterModelsByCapability,
    isWebRelayInterfaceType,
    modelOptionsFromChannels,
    useConfigStore,
    webRelayPresetModels,
    type AiConfig,
    type ModelChannel,
} from "@/stores/use-config-store";
import { testWebRelayConnection, importWebRelayFromBrowser, getWebRelayPoolStatus, type WebRelayPoolStatus } from "@/services/api/webrelay-accounts";
import { ChannelModelSettings } from "./channel-video-pricing";

type UserChannelConnection = "openai" | "gemini" | "deepseek-web" | "qwen-web";
type ChannelSettingsPaneProps = {
    onOpenModels: () => void;
    onOpenRunningHub?: () => void;
};

export function ChannelSettingsPane({ onOpenModels, onOpenRunningHub }: ChannelSettingsPaneProps) {
    const { message } = App.useApp();
    const config = useConfigStore((state) => state.config);
    const replaceConfig = useConfigStore((state) => state.replaceConfig);
    const [loadingChannelIds, setLoadingChannelIds] = useState<string[]>([]);
    const [testingChannelIds, setTestingChannelIds] = useState<string[]>([]);
    const [editingChannelId, setEditingChannelId] = useState<string | null>(null);
    const [newChannelId, setNewChannelId] = useState<string | null>(null);
    const userChannels = config.channels.filter((channel) => channel.scope !== "system");
    const runningHubReady = Boolean(config.runningHub.enabled && config.runningHub.baseUrl.trim() && config.runningHub.apiKey.trim() && config.runningHub.workflowId.trim());

    const updateChannels = (channels: ModelChannel[], baseConfig = config) => {
        replaceConfig(withChannels(baseConfig, channels));
    };

    const updateChannel = (id: string, patch: Partial<ModelChannel>) => {
        updateChannels(config.channels.map((channel) => {
            if (channel.id !== id) return channel;
            const models = patch.models ? uniqueModels(patch.models) : channel.models;
            return {
                ...channel,
                ...patch,
                models,
                modelCosts: patch.modelCosts !== undefined ? patch.modelCosts : (patch.models ? channel.modelCosts?.filter((item) => models.includes(item.model)) : channel.modelCosts),
            };
        }));
    };

    const updateChannelConnection = (channel: ModelChannel, connection: UserChannelConnection) => {
        if (connection === "deepseek-web" || connection === "qwen-web") {
            // 网页中继：凭据是网页版 Token，模型键直传网页端，无需 Base URL。
            const interfaceType = connection === "deepseek-web" ? "webrelay-deepseek" : "webrelay-qwen";
            const previousWeb = channel.interfaceType === "webrelay-deepseek" || channel.interfaceType === "webrelay-qwen" ? channel.interfaceType : "";
            let models = channel.models;
            if (!models.length) {
                models = webRelayPresetModels(interfaceType);
            } else if (previousWeb && previousWeb !== interfaceType) {
                const previousPreset = webRelayPresetModels(previousWeb);
                if (models.length && models.every((model) => previousPreset.includes(model))) {
                    // 模型仍是旧站点预设（没有自定义过）：跟随渠道类型切换到新站点预设。
                    models = webRelayPresetModels(interfaceType);
                }
            }
            updateChannel(channel.id, { apiFormat: "openai", interfaceType, baseUrl: "", models });
            return;
        }
        const apiFormat = connection;
        const defaultBaseUrl = defaultBaseUrlForApiFormat(apiFormat);
        const baseUrl = isKnownDefaultBaseUrl(channel.baseUrl) ? defaultBaseUrl : channel.baseUrl;
        // 渠道只负责连接类型；具体模型能力和请求协议由下方共享能力卡片维护。
        updateChannel(channel.id, { apiFormat, interfaceType: undefined, baseUrl });
    };

    const addChannel = () => {
        const channel = createModelChannel({ name: `渠道 ${userChannels.length + 1}` });
        updateChannels([...config.channels, channel]);
        setNewChannelId(channel.id);
        setEditingChannelId(channel.id);
    };

    const closeChannelEditor = () => {
        setEditingChannelId(null);
        setNewChannelId(null);
    };

    const deleteChannel = (id: string) => {
        const channel = config.channels.find((item) => item.id === id);
        if (channel?.scope === "system") {
            message.warning("系统渠道由管理员维护");
            return;
        }
        updateChannels(config.channels.filter((item) => item.id !== id));
    };

    const setChannelLoading = (id: string, loading: boolean) => {
        setLoadingChannelIds((items) => (loading ? Array.from(new Set([...items, id])) : items.filter((item) => item !== id)));
    };

    const refreshChannelModels = async (channel: ModelChannel) => {
        const connectionError = channelConnectionError(channel);
        if (connectionError) {
            message.error(`${channel.name || "当前渠道"}：${connectionError}`);
            return;
        }
        if (isWebRelayInterfaceType(channel.interfaceType)) {
            // 网页中继没有模型目录接口：重置为本站预设，保留不属于任何站点预设的自定义模型。
            const interfaceType = channel.interfaceType;
            const allPresets = new Set([...webRelayPresetModels("webrelay-deepseek"), ...webRelayPresetModels("webrelay-qwen")]);
            const customs = channel.models.filter((model) => !allPresets.has(model));
            const models = uniqueModels([...webRelayPresetModels(interfaceType), ...customs]);
            updateChannel(channel.id, { models });
            message.success(`${channel.name || "当前渠道"}模型列表已刷新为预设模型（自定义模型已保留）`);
            return;
        }
        setChannelLoading(channel.id, true);
        try {
            const result = await fetchChannelModels(channel, true);
            if (!result.models.length) {
                message.warning(`${channel.name || "当前渠道"}未返回模型，已保留现有手工模型`);
                return;
            }
            const latestConfig = useConfigStore.getState().config;
            const latestChannel = latestConfig.channels.find((item) => item.id === channel.id);
            if (!latestChannel) return;
            if (channelConnectionSignature(latestChannel) !== channelConnectionSignature(channel)) {
                message.warning(`${latestChannel.name || "当前渠道"}的连接配置已改变，已忽略旧的拉取结果`);
                return;
            }
            updateChannels(
                latestConfig.channels.map((item) => (item.id === channel.id ? { ...item, models: result.models, modelCosts: mergeFetchedChannelModelCosts(item, result.catalog) } : item)),
                latestConfig,
            );
            message.success(`${latestChannel.name || "当前渠道"}模型列表已更新`);
        } catch (error) {
            message.error(channelModelFetchErrorMessage(error));
        } finally {
            setChannelLoading(channel.id, false);
        }
    };

    const testChannelConnection = async (channel: ModelChannel) => {
        if (!isWebRelayInterfaceType(channel.interfaceType)) return;
        // Token 框允许留空：一键导入的凭据存在账号池里，后端会自动回退用池里的凭据测试。
        const tokens = channel.apiKey.trim();
        const interfaceType = channel.interfaceType;
        const model = channel.models[0]?.trim() || webRelayPresetModels(interfaceType)[0] || "";
        setTestingChannelIds((items) => Array.from(new Set([...items, channel.id])));
        try {
            const result = await testWebRelayConnection({ interfaceType, tokens, model });
            if (result.ok) {
                const detail = [result.reply ? `回复：${result.reply}` : "", `${result.latencyMs ?? "?"}ms`, `凭据 ${result.tokenIndex ?? 1}/${result.tokenCount ?? 1}`].filter(Boolean).join(" · ");
                message.success(`${channel.name || "当前渠道"}连接成功（${detail}）`);
            } else {
                message.error(`${channel.name || "当前渠道"}连接失败：${result.message || "未知错误"}`);
            }
        } catch (error) {
            message.error(error instanceof Error ? error.message : "测试请求失败");
        } finally {
            setTestingChannelIds((items) => items.filter((id) => id !== channel.id));
        }
    };

    const refreshAllModels = async () => {
        const webChannels = userChannels.filter((channel) => isWebRelayInterfaceType(channel.interfaceType));
        for (const channel of webChannels) {
            const interfaceType = channel.interfaceType;
            if (!channel.models.length && isWebRelayInterfaceType(interfaceType)) updateChannel(channel.id, { models: webRelayPresetModels(interfaceType) });
        }
        const regular = userChannels.filter((channel) => !isWebRelayInterfaceType(channel.interfaceType));
        const runnable = regular.filter((channel) => !channelConnectionError(channel));
        const skipped = regular.filter((channel) => channelConnectionError(channel));
        if (!runnable.length) {
            const detail = skipped.map((channel) => `${channel.name || "未命名渠道"}：${channelConnectionError(channel)}`).join("；");
            message.error(detail || "没有可拉取的个人模型渠道，请先填写有效 Base URL 和 API Key");
            return;
        }
        setChannelLoading("all", true);
        try {
            const results = await Promise.all(
                runnable.map(async (channel) => {
                    try {
                        const result = await fetchChannelModels(channel, true);
                        return { channel, result, error: "" };
                    } catch (error) {
                        return { channel, result: { models: [], catalog: [] }, error: error instanceof Error ? error.message : "读取失败" };
                    }
                }),
            );
            const latestConfig = useConfigStore.getState().config;
            const successful = results.filter((item) => {
                const latestChannel = latestConfig.channels.find((channel) => channel.id === item.channel.id);
                return Boolean(item.result.models.length && latestChannel && channelConnectionSignature(latestChannel) === channelConnectionSignature(item.channel));
            });
            const stale = results.filter((item) => {
                const latestChannel = latestConfig.channels.find((channel) => channel.id === item.channel.id);
                return Boolean(item.result.models.length && (!latestChannel || channelConnectionSignature(latestChannel) !== channelConnectionSignature(item.channel)));
            });
            const failed = results.filter((item) => !item.result.models.length);
            if (successful.length) {
                const resultMap = new Map(successful.map((item) => [item.channel.id, item.result] as const));
                updateChannels(
                    latestConfig.channels.map((channel) => {
                        const fetched = resultMap.get(channel.id);
                        return fetched ? { ...channel, models: fetched.models, modelCosts: mergeFetchedChannelModelCosts(channel, fetched.catalog) } : channel;
                    }),
                    latestConfig,
                );
                message.success(`已更新 ${successful.length} 个渠道的模型`);
            }
            const warnings = [
                ...failed.map((item) => `${item.channel.name || "未命名渠道"}：${item.error || "未返回模型"}`),
                ...stale.map((item) => `${item.channel.name || "未命名渠道"}：连接配置已改变，已忽略旧结果`),
                ...skipped.map((channel) => `${channel.name || "未命名渠道"}：${channelConnectionError(channel)}`),
            ];
            if (warnings.length) message.warning(`${warnings.join("；")}。未更新的渠道已保留原有模型列表`);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "批量读取模型失败，原有模型列表未改动");
        } finally {
            setChannelLoading("all", false);
        }
    };

    return (
        <Form layout="vertical" requiredMark={false}>
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>个人渠道</h2>
                    <p>管理个人模型服务和工作流渠道。普通渠道只保存连接类型；模型能力在“模型与能力”中配置。<Button type="link" size="small" className="h-auto p-0 text-xs font-semibold" onClick={onOpenModels}>打开模型选择</Button></p>
                </div>
                <div className="flex w-full gap-2 sm:w-auto sm:shrink-0">
                    <Button className="h-10 flex-1 sm:h-8 sm:flex-none" icon={<RefreshCw className="size-4" />} loading={loadingChannelIds.includes("all")} disabled={loadingChannelIds.some((id) => id !== "all")} onClick={() => void refreshAllModels()}>拉取全部</Button>
                    <Button className="h-10 flex-1 sm:h-8 sm:flex-none" type="primary" icon={<Plus className="size-4" />} onClick={addChannel}>新增渠道</Button>
                </div>
            </div>
            {onOpenRunningHub ? <section className="settings-section mb-3">
                <div className="mb-3">
                    <h3 className="text-sm font-semibold">个人工作流渠道</h3>
                    <p className="mt-1 text-xs text-foreground/55">RunningHub 使用独立的云端工作流参数与执行通道。</p>
                </div>
                <div className="grid gap-2 lg:grid-cols-2">
                    {onOpenRunningHub ? (
                        <WorkflowChannelEntry
                            icon={<Workflow className="size-4" />}
                            title="RunningHub"
                            description="云端工作流和 RunningHub App"
                            status={runningHubReady ? `${config.runningHub.workflows.length} 个工作流已配置` : config.runningHub.enabled ? "待完成连接和工作流配置" : "未启用"}
                            ready={runningHubReady}
                            onOpen={onOpenRunningHub}
                        />
                    ) : null}
                </div>
            </section> : null}
            {userChannels.length ? (
                <div className="settings-channel-list space-y-2">
                    {userChannels.map((channel) => {
                        const editing = editingChannelId === channel.id;
                        return (
                            <section key={channel.id} aria-labelledby={`channel-${channel.id}-title`} className="settings-channel p-2.5 sm:p-3">
                                <div className="mb-2.5 flex flex-wrap items-start justify-between gap-2.5">
                                    <div className="min-w-0 flex-1 basis-52">
                                        <h3 id={`channel-${channel.id}-title`} className="truncate text-sm font-semibold">{channel.name || "未命名渠道"}</h3>
                                        <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-foreground/55">
                                            {channelProtocolLabel(channel)} · 已保存 {channel.models.length} 个模型
                                            <ChannelStatus channel={channel} />
                                        </div>
                                    </div>
                                    <div className="flex w-full justify-end gap-2 sm:w-auto sm:shrink-0">
                                        <Button className="h-10 sm:h-8" size="small" icon={<RefreshCw className="size-3.5" />} loading={loadingChannelIds.includes(channel.id)} disabled={loadingChannelIds.includes("all")} onClick={() => void refreshChannelModels(channel)}>拉取模型</Button>
                                        <Button size="small" icon={<Pencil className="size-3.5" />} onClick={() => { setNewChannelId(null); setEditingChannelId(channel.id); }}>编辑</Button>
                                        <Popconfirm title="删除个人模型渠道？" description="该渠道关联的模型选择会同时移除。" okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={() => deleteChannel(channel.id)}>
                                            <Tooltip title="删除渠道"><Button className="size-10 p-0 sm:size-8" aria-label={`删除渠道 ${channel.name || "未命名渠道"}`} size="small" type="text" danger disabled={loadingChannelIds.includes(channel.id) || loadingChannelIds.includes("all")} icon={<Trash2 className="size-3.5" />} /></Tooltip>
                                        </Popconfirm>
                                    </div>
                                </div>
                                {editing && (
                                    <ModelEditorModal
                                        open
                                        title={channel.id === newChannelId ? "新增自定义渠道" : "编辑自定义渠道"}
                                        subtitle={channel.name}
                                        onClose={closeChannelEditor}
                                        footer={<div className="model-editor-footer">
                                            <span className="text-xs text-foreground/50">更改实时保存到云端渠道配置</span>
                                            <div className="model-editor-footer-actions">
                                                <Button loading={loadingChannelIds.includes(channel.id)} onClick={() => void refreshChannelModels(channel)}>拉取模型</Button>
                                                <Button type="primary" onClick={closeChannelEditor}>完成</Button>
                                            </div>
                                        </div>}
                                    >
                                        <div className="model-editor-panel">
                                            <section className="model-editor-section">
                                                <div>
                                                    <h2>连接信息</h2>
                                                    <p className="mt-1 text-xs text-foreground/50">用于拉取模型目录并向当前渠道发起请求。</p>
                                                </div>
                                                <div className="model-editor-connection-fields grid gap-3 sm:grid-cols-2">
                                                    <Form.Item label="渠道名称" htmlFor={`channel-${channel.id}-name`} className="mb-0 sm:col-span-1"><Input id={`channel-${channel.id}-name`} value={channel.name} placeholder="例如：我的 NewAPI" onChange={(event) => updateChannel(channel.id, { name: event.target.value })} onBlur={(event) => updateChannel(channel.id, { name: event.target.value.trim() || "未命名渠道" })} /></Form.Item>
                                                    <Form.Item label="渠道类型" className="mb-0 sm:col-span-1" extra="网页中继类型使用网页版账号凭据，不走开放 API。"><Segmented<UserChannelConnection> block value={channelConnectionMode(channel)} options={[{ label: "OpenAI", value: "openai" }, { label: "Gemini", value: "gemini" }, { label: "DeepSeek 网页", value: "deepseek-web" }, { label: "千问 网页", value: "qwen-web" }]} onChange={(value) => updateChannelConnection(channel, value)} /></Form.Item>
                                                    {isWebRelayInterfaceType(channel.interfaceType) ? (
                                                        <>
                                                            <Form.Item label="网页版 Token" htmlFor={`channel-${channel.id}-api-key`} className="mb-0 sm:col-span-2" extra="一行一个，可粘贴多个实现自动轮换；点击下方「如何获取网页版凭据」查看分步教程与一键复制脚本。">
                                                                <Input.TextArea id={`channel-${channel.id}-api-key`} rows={4} autoComplete="new-password" value={channel.apiKey} placeholder={channel.interfaceType === "webrelay-deepseek" ? "粘贴 DeepSeek 网页版 userToken，一行一个" : "粘贴千问网页版整段 Cookie（推荐）或 token，一行一个"} onChange={(event) => updateChannel(channel.id, { apiKey: event.target.value })} onBlur={(event) => updateChannel(channel.id, { apiKey: event.target.value.trim() })} />
                                                            </Form.Item>
                                                            <div className="sm:col-span-2 flex flex-wrap items-center gap-2">
                                                                <Button size="small" icon={<PlugZap className="size-3.5" />} loading={testingChannelIds.includes(channel.id)} onClick={() => void testChannelConnection(channel)}>测试连接</Button>
                                                                <span className="text-xs text-foreground/50">发一条测试消息验证凭据：逐个 Token 尝试，任一成功即通过。</span>
                                                            </div>
                                                            <WebRelayCredentialGuide site={channel.interfaceType === "webrelay-deepseek" ? "deepseek" : "qwen"} />
                                                            <p className="sm:col-span-2 text-xs text-foreground/50">网页中继由本服务代理访问网页版会话：生成走网页凭据池，凭据失效自动切换下一个；模型名直接透传网页端。千问网页中继支持图片/视频附件（多模态，如视频反推）；DeepSeek 网页中继为纯文本。</p>
                                                        </>
                                                    ) : (
                                                        <>
                                                            <Form.Item label="Base URL" htmlFor={`channel-${channel.id}-base-url`} className="mb-0 sm:col-span-1"><Input id={`channel-${channel.id}-base-url`} inputMode="url" value={channel.baseUrl} placeholder="填写云端渠道 Base URL" onChange={(event) => updateChannel(channel.id, { baseUrl: event.target.value })} onBlur={(event) => updateChannel(channel.id, { baseUrl: event.target.value.trim().replace(/\/+$/u, "") })} /></Form.Item>
                                                            <Form.Item label="API Key" htmlFor={`channel-${channel.id}-api-key`} className="mb-0 sm:col-span-1"><Input.Password id={`channel-${channel.id}-api-key`} autoComplete="new-password" value={channel.apiKey} placeholder={channel.apiFormat === "gemini" ? "填写 Gemini API Key" : "填写当前渠道 API Key"} onChange={(event) => updateChannel(channel.id, { apiKey: event.target.value })} onBlur={(event) => updateChannel(channel.id, { apiKey: event.target.value.trim() })} /></Form.Item>
                                                            <Form.Item label="Secret Key（可选）" htmlFor={`channel-${channel.id}-secret-key`} className="mb-0 sm:col-span-1" extra="即梦等 AK/SK 协议需要；其他协议留空。"><Input.Password id={`channel-${channel.id}-secret-key`} autoComplete="new-password" value={channel.secretKey || ""} placeholder="填写 Secret Key" onChange={(event) => updateChannel(channel.id, { secretKey: event.target.value })} onBlur={(event) => updateChannel(channel.id, { secretKey: event.target.value.trim() })} /></Form.Item>
                                                            <div className="sm:col-span-2"><ChannelHeadersEditor value={channel.headers} onChange={(headers) => updateChannel(channel.id, { headers })} /></div>
                                                        </>
                                                    )}
                                                </div>
                                            </section>
                                            <section className="model-editor-section">
                                                <div>
                                                    <h2>模型与能力</h2>
                                                    <p className="mt-1 text-xs text-foreground/50">维护渠道模型，并在单个模型中配置调用协议、能力和定价。</p>
                                                </div>
                                                <Form.Item label="模型列表" htmlFor={`channel-${channel.id}-models`} className="mb-0"><Select id={`channel-${channel.id}-models`} mode="tags" showSearch allowClear maxTagCount="responsive" tokenSeparators={[",", "\n"]} placeholder="输入模型名，或点击拉取模型" value={channel.models} onChange={(models) => updateChannel(channel.id, { models: uniqueModels(models) })} /></Form.Item>
                                                {isWebRelayInterfaceType(channel.interfaceType) ? <p className="mt-2 text-xs text-foreground/50">网页中继渠道的模型名直接透传网页端，无需配置调用协议与定价。</p> : <ChannelModelSettings channel={channel} onChange={(modelCosts) => updateChannel(channel.id, { modelCosts })} />}
                                            </section>
                                        </div>
                                    </ModelEditorModal>
                                )}
                            </section>
                        );
                    })}
                </div>
            ) : <WorkspaceState icon="settings" compact title="当前没有个人模型渠道" description="管理员配置的系统渠道会出现在模型选择中；也可以添加自己的模型服务。" action={<Button icon={<Plus className="size-4" />} onClick={addChannel}>新增个人模型渠道</Button>} />}
        </Form>
    );
}

function WorkflowChannelEntry({ icon, title, description, status, ready, onOpen }: { icon: ReactNode; title: string; description: string; status: string; ready: boolean; onOpen?: () => void }) {
    return (
        <div className="settings-channel flex min-w-0 items-center justify-between gap-3 p-3">
            <div className="flex min-w-0 items-start gap-2.5">
                <span className="mt-0.5 shrink-0 text-[var(--workspace-accent)]" aria-hidden="true">{icon}</span>
                <div className="min-w-0">
                    <h4 className="text-sm font-semibold">{title}</h4>
                    <p className="mt-0.5 truncate text-xs text-foreground/55">{description}</p>
                    <span className={`settings-channel-status mt-1.5 ${ready ? "is-ready" : "is-warning"}`}><i aria-hidden="true" />{status}</span>
                </div>
            </div>
            <Button size="small" onClick={onOpen} disabled={!onOpen}>配置</Button>
        </div>
    );
}

export function channelValidationError(channel: ModelChannel) {
    return channelConnectionError(channel) || validateChannelHeaders(channel.headers) || (!channel.models.length ? "请添加至少一个模型" : "");
}

export function isChannelReady(channel: ModelChannel) {
    return !channelValidationError(channel);
}

export function focusInvalidChannelField(channel: ModelChannel) {
    const baseUrlError = channelConnectionError({ ...channel, apiKey: "valid", secretKey: "valid" });
    const field = baseUrlError ? "base-url" : !channel.apiKey.trim() ? "api-key" : requiresSecretKey(channel) && !channel.secretKey?.trim() ? "secret-key" : "models";
    requestAnimationFrame(() => {
        const element = document.getElementById(`channel-${channel.id}-${field}`);
        element?.scrollIntoView({ behavior: "smooth", block: "center" });
        element?.focus({ preventScroll: true });
    });
}

function ChannelStatus({ channel }: { channel: ModelChannel }) {
    const error = channelValidationError(channel);
    return (
        <span className={`settings-channel-status ${error ? "is-warning" : "is-ready"}`}>
            <i aria-hidden="true" />
            {error || "可用"}
        </span>
    );
}

function withChannels(config: AiConfig, channels: ModelChannel[]): AiConfig {
    const models = modelOptionsFromChannels(channels);
    const imageModels = filterModelsByCapability(models, "image", channels);
    const videoModels = filterModelsByCapability(models, "video", channels);
    const textModels = filterModelsByCapability(models, "text", channels);
    const audioModels = filterModelsByCapability(models, "audio", channels);
    return { ...config, channels, models, baseUrl: channels[0]?.baseUrl || config.baseUrl, apiKey: channels[0]?.apiKey || config.apiKey, apiFormat: channels[0]?.apiFormat || config.apiFormat, imageModels, videoModels, textModels, audioModels, imageModel: normalizeDefaultModel(config.imageModel, imageModels), videoModel: normalizeDefaultModel(config.videoModel, videoModels), textModel: normalizeDefaultModel(config.textModel, textModels), audioModel: normalizeDefaultModel(config.audioModel, audioModels) };
}

function normalizeDefaultModel(value: string, options: string[]) {
    return options.includes(value) ? value : options[0] || "";
}

function uniqueModels(models: string[]) {
    return Array.from(new Set(models.map((model) => model.trim()).filter(Boolean)));
}

function channelModelFetchErrorMessage(error: unknown) {
    const detail = error instanceof Error ? error.message : "读取模型失败";
    if (detail.includes("不允许访问本机") || detail.includes("不允许访问保留地址")) return `${detail}；可信私网服务需由部署管理员配置 CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS`;
    return `${detail}；也可以直接在模型列表中手动输入模型名`;
}

function channelConnectionMode(channel: ModelChannel): UserChannelConnection {
    if (channel.interfaceType === "webrelay-deepseek") return "deepseek-web";
    if (channel.interfaceType === "webrelay-qwen") return "qwen-web";
    return channel.apiFormat === "gemini" ? "gemini" : "openai";
}

function channelConnectionError(channel: ModelChannel) {
    if (isWebRelayInterfaceType(channel.interfaceType)) {
        // Token 框允许留空：凭据可来自账号池（一键导入/书签导入），生成与测试都会自动回退用池。
        return "";
    }
    const baseUrl = channel.baseUrl.trim();
    if (!baseUrl) return "请填写 Base URL";
    try {
        const parsed = new URL(baseUrl);
        if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return "Base URL 只支持 HTTP 或 HTTPS";
    } catch {
        return "Base URL 格式不正确";
    }
    if (!channel.apiKey.trim()) return "请填写 API Key / Access Key";
    if (requiresSecretKey(channel) && !channel.secretKey?.trim()) return "当前协议需要填写 Secret Key";
    return "";
}

function channelConnectionSignature(channel: ModelChannel) {
    return [channel.baseUrl.trim(), channel.apiKey.trim(), channel.secretKey?.trim() || "", channel.apiFormat, JSON.stringify(channel.headers || [])].join("\n");
}

function channelProtocolLabel(channel: ModelChannel) {
    if (channel.interfaceType === "webrelay-deepseek") return "DeepSeek 网页中继";
    if (channel.interfaceType === "webrelay-qwen") return "千问网页中继";
    return channelConnectionMode(channel) === "gemini" ? "Gemini 原生" : "OpenAI 兼容";
}

function isKnownDefaultBaseUrl(value: string) {
    const normalized = value.trim().replace(/\/+$/, "");
    if (!normalized) return true;
    return [defaultBaseUrlForApiFormat("openai"), defaultBaseUrlForApiFormat("gemini")].some((candidate) => candidate.replace(/\/+$/, "") === normalized);
}

function requiresSecretKey(channel: ModelChannel) {
    return channel.modelCosts?.some((item) => item.protocol?.startsWith("volcengine-jimeng-")) === true;
}

type WebRelayGuideSite = "deepseek" | "qwen";

function WebRelayCredentialGuide({ site }: { site: WebRelayGuideSite }) {
    const { message } = App.useApp();
    const [open, setOpen] = useState(false);
    const [manualOpen, setManualOpen] = useState(false);
    const [troubleOpen, setTroubleOpen] = useState(false);
    const [browserBusy, setBrowserBusy] = useState(false);
    const [pool, setPool] = useState<WebRelayPoolStatus | null>(null);
    const refreshPool = async () => {
        try {
            setPool(await getWebRelayPoolStatus(site));
        } catch {
            setPool(null);
        }
    };
    useEffect(() => {
        if (open) void refreshPool();
    }, [open]);
    const importFromBrowser = async () => {
        if (browserBusy) return;
        setBrowserBusy(true);
        // 单次尝试：成功返回 result；返回 null 表示后端在等用户在登录窗口完成登录（已开始轮询）。
        const attempt = async (): Promise<{ message: string } | null> => {
            try {
                return await importWebRelayFromBrowser(site);
            } catch (error) {
                const raw = error instanceof Error ? error.message : "";
                if (raw.startsWith("NEED_LOGIN:")) {
                    message.info(raw.replace("NEED_LOGIN:", ""), 6);
                    return null;
                }
                message.error(raw || "导入失败：请确认已用 Edge/Chrome 登录过网页版后重试");
                throw error;
            }
        };
        try {
            const result = await attempt();
            if (result) {
                message.success(result?.message || "导入成功");
                await refreshPool();
                return;
            }
            // 等待用户在弹出的登录窗口里完成登录：每 4 秒静默重试一次，最长 3 分钟。
            const deadline = Date.now() + 180_000;
            while (Date.now() < deadline) {
                await new Promise((resolve) => setTimeout(resolve, 4000));
                try {
                    const retry = await importWebRelayFromBrowser(site);
                    message.success(retry?.message || "导入成功");
                    await refreshPool();
                    return;
                } catch (error) {
                    const raw = error instanceof Error ? error.message : "";
                    if (raw.startsWith("NEED_LOGIN:")) {
                        continue; // 还没登录完成，继续等
                    }
                    message.error(raw || "导入失败");
                    return;
                }
            }
            message.warning("等待登录超时——登录完成后请再点一次「从浏览器一键导入」");
        } catch {
            // 错误提示已在 attempt 里给出
        } finally {
            setBrowserBusy(false);
        }
    };
    const isDeepSeek = site === "deepseek";
    const siteURL = isDeepSeek ? "https://chat.deepseek.com/" : "https://chat.qwen.ai/";
    const siteName = isDeepSeek ? "DeepSeek" : "千问";
    const origin = typeof window !== "undefined" ? window.location.origin : "http://127.0.0.1:7070";
    const captureScript = `(function(){try{var d=location.host||location.href;if(d.indexOf('deepseek')<0&&d.indexOf('qwen')<0){alert('这个书签要在 DeepSeek/千问网页版标签页里点击才有用。\\n当前页面：'+d+'\\n请先打开 chat.deepseek.com 或 chat.qwen.ai 并登录，再点我。');return}var h=d.indexOf('qwen')>-1?'qwen':'deepseek';var c=h==='qwen'?((document.cookie.match(/(?:^|;\\\s*)token=([^;]*)/)||[])[1]||document.cookie):((localStorage.getItem('userToken')||'').replace(/^"|"$/g,''));if(!c){alert('未找到凭据，请先登录'+(h==='qwen'?'千问':'DeepSeek')+'网页版，再点这个书签');return}fetch('${origin}/api/webrelay-capture',{method:'POST',headers:{'Content-Type':'text/plain'},body:JSON.stringify({site:h,credential:c})}).then(function(r){return r.json()}).then(function(j){var o=j&&j.data;alert(o?('OK: '+(o.duplicate?'该凭据已导入过，登录态已刷新':'导入成功！影策账号池现在有 '+o.total+' 个凭据')):('导入失败：'+((j&&j.msg)||'未知错误')))}).catch(function(e){if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(c).then(function(){alert('直接提交失败('+e+')。\\n凭据已复制到剪贴板——回到影策打开「编辑自定义渠道」，粘贴进「网页版 Token」框即可。')}).catch(function(){alert('导入失败：'+e+'（请确认影策正在运行）')})}else{alert('导入失败：'+e+'（请确认影策正在运行）')}})}catch(e){alert('脚本执行出错：'+e)}})()`;
    const bookmarklet = "javascript:" + encodeURIComponent(captureScript);
    const testBookmarklet = "javascript:" + encodeURIComponent("alert('✅ 书签机制正常！绿色的「一键导入影策」也能这样工作。')");
    const copyText = async (text: string, tip = "已复制") => {
        try {
            await navigator.clipboard.writeText(text);
            message.success(tip);
        } catch {
            message.error("复制失败，请手动选中复制");
        }
    };
    return (
        <div className="sm:col-span-2 rounded-lg border border-foreground/10 bg-foreground/[0.03] p-3 text-xs leading-relaxed">
            <button type="button" className="flex items-center gap-1.5 font-medium text-foreground/80 transition hover:text-foreground" onClick={() => setOpen((value) => !value)}>
                <BookOpen className="size-3.5" />
                一键导入{siteName}凭据（推荐 · 不用碰 F12）
                <ChevronDown className={`size-3.5 transition-transform ${open ? "rotate-180" : ""}`} />
            </button>
            {open && (
                <div className="mt-2.5 space-y-2.5">
                    <div className="rounded-md border border-emerald-500/40 bg-emerald-500/[0.08] p-2.5">
                        <p className="font-medium text-foreground/90">方式一（最推荐）：从浏览器一键导入</p>
                        <p className="mt-1 text-foreground/60">点下面按钮影策就会自动读取{siteName}网页版的登录凭据并存进账号池——不用书签、不用 F12。{!isDeepSeek && "首次使用会弹出一个 Edge 登录窗口，在里面登录一次千问（之后该窗口的登录态会被记住，以后再点就是全自动）。"}</p>
                        <div className="mt-2">
                            <Button type="primary" size="small" loading={browserBusy} onClick={() => void importFromBrowser()} className="bg-emerald-600">📂 从浏览器一键导入{siteName}凭据</Button>
                        </div>
                        {pool && (
                            <p className="mt-2 rounded-md border border-foreground/10 bg-background/70 p-2">
                                {pool.accountCount > 0 ? (
                                    <>✅ 账号池里已有 <b>{pool.accountCount}</b> 个{siteName}凭据（当前可用 {pool.availableCount} 个），最新一条：{pool.accounts[pool.accounts.length - 1]?.label || "—"}（{pool.accounts[pool.accounts.length - 1]?.masked}）。「网页版 Token」框<b>保持留空</b>即可，生成和「测试连接」都会自动用池里的凭据。</>
                                ) : (
                                    <>账号池现在还是空的：点上面按钮导入，或在{siteName}网页版登录后用书签导入。导入后「网页版 Token」框<b>保持留空</b>即可。</>
                                )}
                            </p>
                        )}
                    </div>
                    <p className="text-foreground/50">方式二：书签导入（仅 Chrome 等支持 javascript 书签的浏览器可用；Edge 存在书签脚本点击无反应的官方缺陷，请用方式一）。</p>
                    <GuideStep index={1}><b>10 秒自检</b>：把下面这个橙色按钮<b>拖到书签栏</b>（书签栏没显示按 Ctrl+Shift+B），然后随便在哪个普通网页上点它——弹出「书签机制正常」就说明你的浏览器和拖拽都没问题，继续第 2 步；<b>点了没弹窗 = 你的浏览器不支持书签脚本</b>，直接用最下方的「手动复制获取（备用方式）」。
                        <div className="mt-1.5">
                            <a href={testBookmarklet} draggable onClick={(event) => { event.preventDefault(); message.info("请把我拖到书签栏，然后随便打开一个网页点我测试"); }} className="inline-flex cursor-grab items-center gap-1 rounded-md bg-amber-500 px-2.5 py-1.5 text-[11px] font-semibold text-white shadow-sm select-none active:cursor-grabbing">🧪 书签自检</a>
                        </div>
                    </GuideStep>
                    <GuideStep index={2}>把下面这个按钮<b>拖到书签栏</b>（和自检按钮并排放就行）。拖完后可右键书签 →「编辑」检查：网址必须以 <code>javascript:</code> 开头才有效。
                        <div className="mt-1.5">
                            <a href={bookmarklet} draggable onClick={(event) => { event.preventDefault(); message.info("请把我拖到浏览器书签栏，之后在网页版页面点击我"); }} className="inline-flex cursor-grab items-center gap-1 rounded-md bg-emerald-600 px-2.5 py-1.5 text-[11px] font-semibold text-white shadow-sm select-none active:cursor-grabbing">📥 一键导入影策</a>
                        </div>
                    </GuideStep>
                    <GuideStep index={3}><a href={siteURL} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">点这里打开{siteName}网页版</a>，登录账号，然后<b>就在这个网页版标签页里</b>点书签栏的「📥 一键导入影策」—— 弹出「导入成功」即完成，凭据已自动存入影策账号池。</GuideStep>
                    <GuideStep index={4}>回到本窗口：「网页版 Token」框<b>留空即可</b>（自动使用账号池里的凭据），点「测试连接」验证。</GuideStep>
                    <p className="text-foreground/50">多个账号：分别登录后各点一次书签即可叠加进池，生成时自动轮换；同一凭据重复导入会自动刷新登录态、清掉过期标记。</p>
                    <button type="button" className="text-foreground/50 underline underline-offset-2 hover:text-foreground/80" onClick={() => setTroubleOpen((value) => !value)}>⚠ 点了书签没反应？</button>
                    {troubleOpen && (
                        <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/[0.06] p-2">
                            <p><b>1. 先跑第 1 步的「🧪 书签自检」</b>：把橙色按钮拖到书签栏后随便找个网页点它。没弹窗 = 浏览器不支持/禁用了书签脚本（或拖拽没成功），直接换 Chrome / Edge，或用最下方「手动复制获取（备用方式）」。有弹窗 = 书签机制正常，看第 2 条。</p>
                            <p><b>2. 检查书签是否被弄坏</b>：右键书签栏里的「📥 一键导入影策」→「编辑」，网址必须以 <code>javascript:</code> 开头。凡是复制粘贴创建的、或以 <code>%28function</code> 等乱码开头的，都是被浏览器剥掉前缀的坏书签——<b>删掉，回到本页用鼠标把绿色按钮拖到书签栏重建</b>。</p>
                            <p><b>3. 别用地址栏测试书签</b>：把 javascript: 链接粘到地址栏，浏览器一律会剥掉前缀再去访问，看到乱码或一串数据是正常现象，不代表书签坏了——书签好不好只能靠「点击」来验证。</p>
                            <p><b>4. 要在网页版标签页里点</b>：切到 {siteName} 网页版（chat.{isDeepSeek ? "deepseek" : "qwen"}.ai）的标签页再点书签。新版书签在任何普通页面点击都会弹窗说明原因。</p>
                        </div>
                    )}
                    <button type="button" className="text-foreground/50 underline underline-offset-2 hover:text-foreground/80" onClick={() => setManualOpen((value) => !value)}>手动复制获取（备用方式）</button>
                    {manualOpen && (
                        <div className="space-y-2 rounded-md border border-foreground/10 bg-background/60 p-2">
                            {isDeepSeek ? (
                                <>
                                    <p>登录 chat.deepseek.com 后按 F12 打开控制台，运行：</p>
                                    <GuideSnippet code={'copy((localStorage.getItem("userToken")||"").replace(/^"|"$/g,""))'} onCopy={() => void copyText('copy((localStorage.getItem("userToken")||"").replace(/^"|"$/g,""))', "脚本已复制，去网页版控制台粘贴回车")} />
                                    <p>回车后 Token 自动进剪贴板，粘贴到上方「网页版 Token」框。</p>
                                </>
                            ) : (
                                <>
                                    <p>登录 chat.qwen.ai 后按 F12 打开控制台，运行（推荐整段 Cookie 过风控）：</p>
                                    <GuideSnippet code="copy(document.cookie)" onCopy={() => void copyText("copy(document.cookie)", "脚本已复制，去网页版控制台粘贴回车")} />
                                    <p>回车后 Cookie 自动进剪贴板，粘贴到上方「网页版 Token」框；只取 token 也可运行：</p>
                                    <GuideSnippet code={'copy((document.cookie.match(/(?:^|;\\s*)token=([^;]*)/)||[])[1]||"")'} onCopy={() => void copyText('copy((document.cookie.match(/(?:^|;\\s*)token=([^;]*)/)||[])[1]||"")', "脚本已复制，去网页版控制台粘贴回车")} />
                                </>
                            )}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}

function GuideStep({ index, children }: { index: number; children: ReactNode }) {
    return (
        <div className="flex gap-2">
            <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full bg-foreground/10 text-[10px] font-semibold text-foreground/80">{index}</span>
            <div className="min-w-0 flex-1">{children}</div>
        </div>
    );
}

function GuideSnippet({ code, onCopy }: { code: string; onCopy: () => void }) {
    return (
        <div className="mt-1.5 flex items-center gap-2 rounded-md border border-foreground/10 bg-background/60 px-2 py-1.5">
            <code className="min-w-0 flex-1 break-all font-mono text-[11px] text-foreground/80">{code}</code>
            <Button size="small" onClick={onCopy}>复制</Button>
        </div>
    );
}

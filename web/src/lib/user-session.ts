import { getFeatureAvailability, type AuthSessionPayload } from "@/services/api/auth";
import { getModelCatalog, type CapabilitySpec, type ModelCatalogResponse, type OptionConstraint, type PublicChannelCatalog } from "@/services/api/logical-models";
import { localForageStorage } from "@/lib/localforage-storage";
import { appQueryClient } from "@/lib/query-client";
import { scopedLocalStorage, setActiveUserScope } from "@/lib/user-scope";
import { CANVAS_STORE_KEY, flushCanvasStorePersistence, useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { CANVAS_HISTORY_STORE_KEY, useCanvasHistoryStore } from "@/stores/canvas/use-canvas-history-store";
import { ASSET_STORE_KEY, flushAssetStorePersistence, useAssetStore } from "@/stores/use-asset-store";
import { CONFIG_STORE_KEY, defaultConfig, normalizeConfigSnapshot, useConfigStore, type ModelCapability, type ModelChannel } from "@/stores/use-config-store";
import { CREATION_PREFERENCES_STORE_KEY, useCreationPreferencesStore } from "@/stores/use-creation-preferences-store";
import { defaultModelCapabilityConfig, STANDARD_IMAGE_SIZE_VALUES, type ModelCapabilityConfig } from "@/lib/model-capabilities";
import { imageSizeConfigWithPresets } from "@/lib/image-size-presets";
import { useUserStore } from "@/stores/use-user-store";
import { PLUGIN_STORE_KEY, usePluginStore } from "@/stores/use-plugin-store";
import { initializeRemoteUserDataSession, installRemoteUserDataAutoSync, resetRemoteUserDataSync, withRemoteUserDataSyncExclusive } from "@/services/user-data-sync";
import { withGenerationConsumersPaused } from "@/services/generation-consumer-lifecycle";

export async function switchUserStorageScope(userId?: string | null) {
    await withGenerationConsumersPaused(async () => {
        await withRemoteUserDataSyncExclusive(async () => {
            await Promise.all([flushCanvasStorePersistence(), flushAssetStorePersistence()]);
            resetRemoteUserDataSync();
            setActiveUserScope(userId);
        });
    });
}

export async function applyUserSession(payload: AuthSessionPayload) {
    const previousUserId = useUserStore.getState().user?.id || "";
    const nextUserId = payload.user?.id || "";
    useUserStore.getState().setHydrated(false);
    try {
        // Query key 不携带用户 ID；身份变化时必须取消并清空旧账号请求，避免跨账号复用内存数据。
        if (previousUserId !== nextUserId) appQueryClient.clear();
        await switchUserStorageScope(payload.user?.id);
        const [persistedCanvas, persistedCanvasHistory, persistedAssets, persistedPlugins] = await Promise.all([
            localForageStorage.getItem(CANVAS_STORE_KEY),
            localForageStorage.getItem(CANVAS_HISTORY_STORE_KEY),
            localForageStorage.getItem(ASSET_STORE_KEY),
            localForageStorage.getItem(PLUGIN_STORE_KEY),
        ]);
        const persistedConfig = scopedLocalStorage.getItem(CONFIG_STORE_KEY);
        const persistedCreationPreferences = scopedLocalStorage.getItem(CREATION_PREFERENCES_STORE_KEY);
        usePluginStore.setState({ hydrated: false, runtimeStatuses: {}, pluginStates: {} });
        useUserStore.getState().setUser(payload.user);
        useUserStore.getState().setLocalMode(Boolean(payload.localMode));
        useUserStore.getState().setRuntimeLimits(payload.runtimeLimits);
        useUserStore.getState().setDrawingEngine(payload.drawingEngine);
        useUserStore.getState().setFeatures(payload.features);
        await Promise.all([
            useCanvasStore.persist.rehydrate(),
            useCanvasHistoryStore.persist.rehydrate(),
            useAssetStore.persist.rehydrate(),
            useConfigStore.persist.rehydrate(),
            usePluginStore.persist.rehydrate(),
            useCreationPreferencesStore.persist.rehydrate(),
        ]);
        // Zustand 在目标 scope 没有快照时会保留旧内存，必须显式恢复该 scope 的空状态。
        if (!persistedCanvas) useCanvasStore.setState({ projects: [] });
        if (!persistedCanvasHistory) useCanvasHistoryStore.setState({ deletedProjects: [] });
        if (!persistedAssets) useAssetStore.setState({ assets: [] });
        if (!persistedPlugins) usePluginStore.setState({ installations: [], runtimeStatuses: {}, pluginStates: {} });
        if (!persistedCreationPreferences) useCreationPreferencesStore.setState({ preferences: {} });
        if (!persistedConfig) {
            // 只有首次配置缺失时才生成能力推荐；已有配置中的空数组代表用户明确清空。
            const catalog = await getModelCatalog();
            const initialSystemConfig = {
                ...defaultConfig,
                channels: modelCatalogChannels(catalog),
                imageModels: undefined,
                videoModels: undefined,
                textModels: undefined,
                audioModels: undefined,
            };
            useConfigStore.getState().replaceConfig(normalizeConfigSnapshot({ config: initialSystemConfig }).config);
        } else {
            const catalog = await getModelCatalog();
            useConfigStore.getState().mergeSystemChannels(modelCatalogChannels(catalog));
        }
        installRemoteUserDataAutoSync();
        if (payload.user?.id) {
            await initializeRemoteUserDataSession(payload.user.id);
        } else resetRemoteUserDataSync();
    } finally {
        useUserStore.getState().setHydrated(true);
    }
}

export async function refreshSystemChannels() {
    const catalog = await getModelCatalog();
    useConfigStore.getState().mergeSystemChannels(modelCatalogChannels(catalog));
}

// 目录仅接受系统渠道模型，避免畸形响应被当成空目录写入配置。
function modelCatalogChannels(catalog: ModelCatalogResponse): ModelChannel[] {
    if (catalog.source === "system") {
        if (!Array.isArray(catalog.channels)) throw new Error("模型目录响应缺少系统渠道列表");
        // 豆包 / Dola 账号池通道永远可用且排在最前：未配置任何渠道时，
        // 文生图 / 文生视频默认选中豆包（normalizeSelectedModel 回退到 options[0]）。
        return [doubaoPoolChannel(), dolaPoolChannel(), ...systemChannelModelChannels(catalog.channels)];
    }
    throw new Error("模型目录响应来源无效");
}

export const DOUBAO_POOL_CHANNEL_ID = "doubao-pool";
export const DOUBAO_IMAGE_MODEL = "doubao-seedream-image";
export const DOUBAO_VIDEO_MODEL_MINI = "doubao-seedance-video-mini";
export const DOUBAO_VIDEO_MODEL_FAST = "doubao-seedance-video-fast";
/** 历史默认视频模型键（等价 Mini），仅用于旧配置兼容。 */
export const DOUBAO_VIDEO_MODEL = DOUBAO_VIDEO_MODEL_MINI;

export const DOLA_POOL_CHANNEL_ID = "dola-pool";
export const DOLA_VIDEO_MODEL_1_0 = "dola-seedance-video-1.0";
export const DOLA_VIDEO_MODEL_2_5 = "dola-seedance-video-2.5";
export const DOLA_VIDEO_MODEL_FAST = "dola-seedance-video-fast";

/**
 * 豆包视频模型能力：按 2026-09-19 账号池限制表收口——
 * 单次 15 秒、上限 5 条、720P、多参考图。
 * 其余能力字段沿用全局默认，避免部分字段缺失导致归一化崩溃。
 */
function doubaoPoolVideoCapabilityConfig(): ModelCapabilityConfig {
    const config = defaultModelCapabilityConfig();
    if (!config.video) return config;
    return {
        ...config,
        video: {
            ...config.video,
            duration: { ...config.video.duration, min: 1, max: 15 },
            resolutions: ["720p"],
            defaultResolution: "720p",
            maxOutputs: 5,
            referenceMode: "multi",
            operations: Array.from(new Set([...(config.video.operations || ["text_to_video", "image_to_video"]), "reference_to_video"])),
            references: { ...config.video.references, maxImages: 4 },
        },
    };
}

/**
 * Dola 视频模型能力：单次 4-30 秒、上限 2 条、720P、多参考图。
 * defaultSeconds 用于 2.5 档固定 30 秒默认时长；其余档位沿用全局默认。
 */
function dolaPoolVideoCapabilityConfig(defaultSeconds?: number): ModelCapabilityConfig {
    const config = defaultModelCapabilityConfig();
    if (!config.video) return config;
    const duration: NonNullable<ModelCapabilityConfig["video"]>["duration"] = { ...config.video.duration, min: 4, max: 30 };
    if (defaultSeconds) duration.default = Math.min(defaultSeconds, 30);
    return {
        ...config,
        video: {
            ...config.video,
            duration,
            resolutions: ["720p"],
            defaultResolution: "720p",
            maxOutputs: 2,
            referenceMode: "multi",
            operations: Array.from(new Set([...(config.video.operations || ["text_to_video", "image_to_video"]), "reference_to_video"])),
            references: { ...config.video.references, maxImages: 4 },
        },
    };
}

/** 内置「豆包账号池」系统渠道：不需要 API Key，凭据来自账号池。 */
function doubaoPoolChannel(): ModelChannel {
    return {
        id: DOUBAO_POOL_CHANNEL_ID,
        name: "豆包账号池",
        baseUrl: "/api",
        apiKey: "pool",
        apiFormat: "openai",
        interfaceType: "doubao-pool",
        scope: "system",
        enabled: true,
        models: [DOUBAO_IMAGE_MODEL, DOUBAO_VIDEO_MODEL_MINI, DOUBAO_VIDEO_MODEL_FAST],
        modelAliases: {},
        modelCosts: [
            {
                model: DOUBAO_IMAGE_MODEL,
                displayName: "豆包生图（Seedream）",
                description: "文生图默认走豆包账号池，自动取号轮换。",
                capability: "image",
                pricePolicy: "channel",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
                capabilityConfig: defaultModelCapabilityConfig(),
            },
            {
                model: DOUBAO_VIDEO_MODEL_MINI,
                displayName: "豆包生视频 Mini（Seedance 2.0）",
                description: "Seedance 2.0 Mini 档，走豆包账号池，自动取号轮换。",
                capability: "video",
                pricePolicy: "channel",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
                capabilityConfig: doubaoPoolVideoCapabilityConfig(),
            },
            {
                model: DOUBAO_VIDEO_MODEL_FAST,
                displayName: "豆包生视频 Fast（Seedance 2.0）",
                description: "Seedance 2.0 Fast 档，出片更快，走豆包账号池。",
                capability: "video",
                pricePolicy: "channel",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
                capabilityConfig: doubaoPoolVideoCapabilityConfig(),
            },
        ],
    };
}

/** 内置「Dola 账号池」系统渠道：与豆包账号池同一套 samantha 协议，仅站点不同。 */
function dolaPoolChannel(): ModelChannel {
    return {
        id: DOLA_POOL_CHANNEL_ID,
        name: "Dola账号池",
        baseUrl: "/api",
        apiKey: "pool",
        apiFormat: "openai",
        interfaceType: "doubao-pool",
        scope: "system",
        enabled: true,
        models: [DOLA_VIDEO_MODEL_1_0, DOLA_VIDEO_MODEL_2_5, DOLA_VIDEO_MODEL_FAST],
        modelAliases: {},
        modelCosts: [
            {
                model: DOLA_VIDEO_MODEL_1_0,
                displayName: "Dola生视频 1.0（Seedance 1.0）",
                description: "Seedance 1.0 档，单次≤30s · 每日2条（跨天自动恢复），走 Dola 账号池。",
                capability: "video",
                pricePolicy: "channel",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
                capabilityConfig: dolaPoolVideoCapabilityConfig(),
            },
            {
                model: DOLA_VIDEO_MODEL_2_5,
                displayName: "Dola生视频 2.5（Seedance 2.5）",
                description: "Seedance 2.5 档，单条 30 秒 · 每日 2 条（跨天自动恢复），走 Dola 账号池。",
                capability: "video",
                pricePolicy: "channel",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
                capabilityConfig: dolaPoolVideoCapabilityConfig(30),
            },
            {
                model: DOLA_VIDEO_MODEL_FAST,
                displayName: "Dola生视频 Fast（Seedance 2.0）",
                description: "Seedance 2.0 Fast 档，单次≤30s · 每日2条（跨天自动恢复），走 Dola 账号池。",
                capability: "video",
                pricePolicy: "channel",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
                capabilityConfig: dolaPoolVideoCapabilityConfig(),
            },
        ],
    };
}

// 系统渠道模型转换为前端配置格式
export function systemChannelModelChannels(channels: PublicChannelCatalog[]): ModelChannel[] {
    return channels.map((channel) => {
        const availableModels = channel.models.filter((m) => m.available);
        return {
            id: channel.id,
            name: channel.displayName,
            sortOrder: channel.sortOrder,
            // 系统渠道必须走带渠道 ID 的站内代理；/api 只是业务 API 根路径，
            // 不能作为模型请求的运行时 Base URL 传给 channelRequest。
            baseUrl: `/api/${channel.id}`,
            apiKey: "system",
            apiFormat: "openai",
            scope: "system" as const,
            enabled: true,
            models: availableModels.map((m) => m.modelKey),
            modelAliases: {},
            modelCosts: availableModels.map((model) => {
                // 标量价格仅用于旧配置兼容；创作端按完整 SKU 档位展示和匹配价格。
                const firstTier = model.priceTiers?.[0];
                const unitPrice = firstTier?.unitPriceMicrocredits || 0;
                const inputPrice = firstTier?.inputTokenPriceMicrocredits || 0;
                const outputPrice = firstTier?.outputTokenPriceMicrocredits || 0;
                const cachedPrice = firstTier?.cachedTokenPriceMicrocredits || 0;
                const billingMode = firstTier?.billingMode || "fixed_request";
                const logicalPriceTiers = (model.priceTiers || []).map((tier) => ({
                    selector: tier.selector || {},
                    resolution: tier.resolution || "*",
                    videoSeconds: tier.videoSeconds || 0,
                    billingMode: tier.billingMode as "fixed_request" | "per_second" | "token",
                    unitPriceMicrocredits: tier.unitPriceMicrocredits || 0,
                    inputTokenPriceMicrocredits: tier.inputTokenPriceMicrocredits || 0,
                    outputTokenPriceMicrocredits: tier.outputTokenPriceMicrocredits || 0,
                    cachedTokenPriceMicrocredits: tier.cachedTokenPriceMicrocredits || 0,
                }));

                return {
                    model: model.modelKey,
                    displayName: model.displayName,
                    channelLabel: model.channelLabel,
                    description: model.description || "",
                    icon: model.icon || "",
                    capability: model.capability as ModelCapability,
                    protocol: model.protocol as any,
                    pricePolicy: "channel" as const,
                    billingMode: billingMode as any,
                    unitPriceMicrocredits: unitPrice,
                    inputTokenPriceMicrocredits: inputPrice,
                    outputTokenPriceMicrocredits: outputPrice,
                    cachedTokenPriceMicrocredits: cachedPrice,
                    capabilityConfig: (model.capabilityConfig as ModelCapabilityConfig | undefined) || defaultModelCapabilityConfig(),
                    channelModelId: model.id,
                    channelId: channel.id,
                    modelKey: model.modelKey,
                    logicalPriceTiers,
                };
            }),
        };
    });
}

export function projectLogicalCapability(spec: CapabilitySpec, defaults: Record<string, unknown>): ModelCapabilityConfig {
    const projected = defaultModelCapabilityConfig();
    if (spec.capability === "image" && projected.image) {
        projected.image.references.maxImages = spec.inputs?.image?.max ?? 0;
        projected.image.references.maskSupported = (spec.inputs?.mask?.max ?? 0) > 0;
        projected.image.size = { parameter: "none", values: [], default: "auto", allowCustom: false };
        projected.image.quality = { supported: false, values: [], default: "auto" };
        projected.image.transparentBackground = { supported: false, default: false };
        const sizeOption = spec.options?.size || spec.options?.aspectRatio;
        const sizeValues = stringValues(sizeOption);
        const sizeAllowsCustom = sizeValues.includes("*") || Boolean(spec.imageSize?.allowCustom);
        const concreteSizeValues = sizeValues.filter((value) => value !== "*");
        const sizePresets = concreteSizeValues.length ? concreteSizeValues : sizeAllowsCustom ? [...STANDARD_IMAGE_SIZE_VALUES] : [];
        if (sizePresets.length || sizeAllowsCustom || spec.imageSize?.presets?.length) {
            const parameter = spec.imageSize?.parameter === "aspect_ratio" || spec.imageSize?.parameter === "size" ? spec.imageSize.parameter : "size";
            projected.image.size = { parameter, values: sizePresets, default: concreteDefault(defaults.size, sizePresets, "1:1"), allowCustom: sizeAllowsCustom };
            if (spec.imageSize?.presets?.length) projected.image.size = imageSizeConfigWithPresets(projected.image, spec.imageSize.presets);
        }
        applyStringOption(spec.options?.quality, defaults.quality, (values, initial) => {
            projected.image!.quality = { supported: true, values, default: initial };
        });
        projected.image.maxOutputs = maxNumericOption(spec.options?.count, 1);
        projected.image.transparentBackground = booleanOption(spec.options?.transparentBackground, defaults.transparentBackground);
    }
    if (spec.capability === "video" && projected.video) {
        projected.video.references.minImages = spec.inputs?.image?.min ?? 0;
        projected.video.references.maxImages = spec.inputs?.image?.max ?? 0;
        projected.video.references.maxVideos = spec.inputs?.video?.max ?? 0;
        projected.video.references.maxAudios = spec.inputs?.audio?.max ?? 0;
        projected.video.operations = spec.operations || [];
        projected.video.defaultOperation = spec.operations?.[0] || "";
        const duration = spec.options?.videoSeconds || spec.options?.duration;
        if (duration?.values?.length) projected.video.duration = { selection: "enum", values: duration.values.map(Number).filter(Number.isFinite), default: Number(defaults.videoSeconds ?? duration.values[0]) };
        else if (duration?.min !== undefined && duration.max !== undefined) projected.video.duration = { selection: "range", min: duration.min, max: duration.max, step: duration.step || 1, default: Number(defaults.videoSeconds ?? duration.min) };
        projected.video.ratios = stringValues(spec.options?.size || spec.options?.aspectRatio);
        projected.video.defaultRatio = concreteDefault(defaults.size, projected.video.ratios, "");
        projected.video.resolutions = stringValues(spec.options?.vquality || spec.options?.resolution);
        projected.video.defaultResolution = String(defaults.vquality ?? projected.video.resolutions[0] ?? "");
        projected.video.generateAudio = booleanOption(spec.options?.videoGenerateAudio, defaults.videoGenerateAudio);
        projected.video.watermark = booleanOption(spec.options?.videoWatermark, defaults.videoWatermark);
    }
    return projected;
}

function applyStringOption(option: OptionConstraint | undefined, fallback: unknown, apply: (values: string[], initial: string) => void) {
    const values = stringValues(option);
    if (values.length) apply(values, concreteDefault(fallback, values, values[0]));
}

function stringValues(option?: OptionConstraint) {
    return (option?.values || [])
        .map(String)
        .map((value) => value.trim())
        .filter(Boolean);
}

function concreteDefault(value: unknown, values: string[], fallback: string) {
    const candidate = String(value ?? "").trim();
    return candidate && candidate !== "*" && values.includes(candidate) ? candidate : values.find((item) => item !== "*") || fallback;
}

function maxNumericOption(option: OptionConstraint | undefined, fallback: number) {
    if (option?.max !== undefined) return option.max;
    const values = (option?.values || []).map(Number).filter(Number.isFinite);
    return values.length ? Math.max(...values) : fallback;
}

function booleanOption(option: OptionConstraint | undefined, fallback: unknown) {
    const supported = (option?.values || []).some((value) => value === true || value === "true");
    return { supported, default: supported && String(fallback) === "true" };
}

export async function refreshFeatureAvailability() {
    const payload = await getFeatureAvailability();
    useUserStore.getState().setFeatures(payload.features);
    return payload.features;
}

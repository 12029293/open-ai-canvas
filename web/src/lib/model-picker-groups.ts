import { configuredModelDisplayName, groupModelsByDisplayName, isDirectSystemModel, type DisplayModelGroup } from "@/lib/model-selection";
import { modelIcon, modelOptionName, PUBLIC_MODEL_CATALOG_ID, resolveModelChannel, type AiConfig } from "@/stores/use-config-store";

export type ModelPickerGroup = {
    key: string;
    label: string;
    icon: string;
    scope: string;
    kind: "product" | "channel";
    models: DisplayModelGroup[];
};

export { isDirectSystemModel } from "@/lib/model-selection";

export function modelChannelLabel(config: AiConfig, value: string) {
    const channel = resolveModelChannel(config, value);
    const cost = channel.modelCosts?.find((item) => item.model === modelOptionName(value));
    return cost?.channelLabel?.trim() || channel.name || "未命名渠道";
}

// 一级按渠道聚合（品牌 = 渠道名，如 豆包账号池 / Dola账号池），二级列渠道内模型。
// 与极简版一致：平台内置渠道统一展示「平台服务」，不再把渠道内每个模型拆成独立品牌。
export function groupModelsForPicker(config: AiConfig, options: string[]): ModelPickerGroup[] {
    const groups = new Map<string, ModelPickerGroup>();
    for (const channel of config.channels) {
        const models = options.filter((value) => resolveModelChannel(config, value).id === channel.id);
        if (!models.length) continue;
        const key = JSON.stringify(["channel", channel.id]);
        groups.set(key, {
            key,
            label: channel.name || "未命名渠道",
            icon: modelIcon(config, models[0]),
            scope: channel.id === PUBLIC_MODEL_CATALOG_ID ? "" : channel.scope === "system" ? "平台服务" : "我的模型",
            kind: "channel",
            models: groupModelsByDisplayName(config, models),
        });
    }
    return Array.from(groups.values());
}

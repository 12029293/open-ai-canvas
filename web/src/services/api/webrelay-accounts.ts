import { http } from "@/services/api/request";

/**
 * 网页中继（DeepSeek 网页版 / 千问网页版）相关 API。
 */

export type WebRelayTestResult = {
    ok: boolean;
    latencyMs?: number;
    tokenIndex?: number;
    tokenCount?: number;
    reply?: string;
    message?: string;
};

export async function testWebRelayConnection(input: { interfaceType: string; tokens: string; model?: string }) {
    return http.post<WebRelayTestResult>("/webrelay-accounts/test", input);
}

export type WebRelayBrowserImportResult = {
    ok: boolean;
    imported: number;
    duplicated: number;
    total: number;
    found: number;
    message: string;
};

// 从本机 Edge/Chrome 浏览器数据里直接提取网页版登录凭据并导入账号池
// （绕开 Edge 不支持 javascript 书签的问题，无需 F12、无需书签）。
export async function importWebRelayFromBrowser(site: string) {
    return http.post<WebRelayBrowserImportResult>("/webrelay-accounts/import-browser", { site });
}

export type WebRelayPoolAccount = {
    id: string;
    site: string;
    label: string;
    masked: string;
    state: string;
    statusText: string;
    enabled: boolean;
    loginExpired: boolean;
    successCount: number;
    useCount: number;
    /** 绑定的出网代理（network-proxies.id，空 = 直连）。 */
    proxyId: string;
};

export type WebRelayPoolStatus = {
    site: string;
    accountCount: number;
    availableCount: number;
    coolingCount: number;
    expiredCount: number;
    disabledCount: number;
    accounts: WebRelayPoolAccount[];
};

// 查询内置账号池状态（一键导入的凭据都存在这里）。
export async function getWebRelayPoolStatus(site: string) {
    return http.get<WebRelayPoolStatus>("/webrelay-accounts", { params: { site } });
}

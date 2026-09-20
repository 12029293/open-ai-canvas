import { http } from "@/services/api/request";

/**
 * 网络代理 API：账号池账号可绑定的出站代理（HTTP / HTTPS / SOCKS5）。
 * 密码不回显，列表只带 hasPassword 标记。
 */

export type ProxyProtocol = "http" | "https" | "socks5";

export type NetworkProxyView = {
    id: string;
    name: string;
    protocol: ProxyProtocol;
    host: string;
    port: number;
    username?: string;
    hasPassword: boolean;
    remark: string;
    createdAt: string;
    updatedAt: string;
};

export type NetworkProxyUpsertRequest = {
    name: string;
    protocol: ProxyProtocol;
    host: string;
    port: number;
    username: string;
    password?: string;
    clearPassword?: boolean;
    remark: string;
};

export type NetworkProxyTestResult = {
    ok: boolean;
    latencyMs: number;
    message: string;
};

/** 代理选项展示：名称（地址），与账号池下拉一致。 */
export function proxyOptionLabel(proxy: Pick<NetworkProxyView, "name" | "host" | "port">): string {
    return `${proxy.name}（${proxy.host}:${proxy.port}）`;
}

export async function fetchNetworkProxies(): Promise<{ proxies: NetworkProxyView[] }> {
    return http.get("/network-proxies");
}

export async function createNetworkProxy(req: NetworkProxyUpsertRequest): Promise<{ proxy: NetworkProxyView }> {
    return http.post("/network-proxies", req);
}

export async function updateNetworkProxy(id: string, req: NetworkProxyUpsertRequest): Promise<{ proxy: NetworkProxyView }> {
    return http.patch(`/network-proxies/${id}`, req);
}

export async function deleteNetworkProxy(id: string): Promise<void> {
    await http.delete(`/network-proxies/${id}`);
}

export async function testNetworkProxy(id: string): Promise<{ result: NetworkProxyTestResult }> {
    return http.post(`/network-proxies/${id}/test`);
}

/** 账号池类型：doubao（豆包/Dola 账号池）与 webrelay（网页中继账号池）。 */
export type ProxyPoolType = "doubao" | "webrelay";

/** 给账号池账号绑定/解绑代理（proxyId 为空 = 恢复直连）。 */
export async function assignNetworkProxy(poolType: ProxyPoolType, ids: string[], proxyId: string): Promise<{ affected: number }> {
    return http.post("/network-proxies/assign", { poolType, ids, proxyId });
}

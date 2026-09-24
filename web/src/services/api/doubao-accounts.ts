import { http } from "@/services/api/request";

/**
 * 豆包账号池 API。完整 Cookie 只保存在后端账号池服务（SQLite），
 * 接口返回的账号视图一律是脱敏后的掩码。
 */

export type DoubaoAccountState = "ready" | "cooling" | "expired" | "disabled";

/** 账号池站点：同一张账号池表承载多站点账号。 */
export type PoolSite = "doubao" | "dola" | "jimeng";

export const POOL_SITE_META: Record<PoolSite, { label: string; url: string }> = {
    doubao: { label: "豆包", url: "https://www.doubao.com/chat/" },
    dola: { label: "Dola", url: "https://www.dola.com/chat/" },
    jimeng: { label: "即梦", url: "https://jimeng.jianying.com/" },
};

export type DoubaoAccountView = {
    id: string;
    site: PoolSite;
    label: string;
    masked: string;
    active: boolean;
    state: DoubaoAccountState;
    statusText: string;
    cooldownUntil: string | null;
    cooldownRemainingText: string;
    enabled: boolean;
    loginExpired: boolean;
    lastError: string;
    proxyId: string;
    hasFullCookie: boolean;
    successCount: number;
    quotaExhaustedAt: string | null;
    source: string;
    tags: string[];
    note: string;
    useCount: number;
    failCount: number;
    lastUsedAt: string | null;
    updatedAt: string;
};

export type DoubaoPoolStatus = {
    site: PoolSite;
    accountCount: number;
    availableCount: number;
    coolingCount: number;
    expiredCount: number;
    disabledCount: number;
    totalSuccess: number;
    totalFail: number;
    tags: string[];
    accounts: DoubaoAccountView[];
    checkedAt: string;
};

export type DoubaoBulkImportResult = {
    added: number;
    updated: number;
    failed: { line: number; reason: string }[];
    total: number;
};

export async function fetchDoubaoPoolStatus(site?: PoolSite) {
    return http.get<DoubaoPoolStatus>("/doubao-accounts", site ? { params: { site } } : undefined);
}

export async function addDoubaoAccount(input: { cookie: string; site?: PoolSite; label?: string; note?: string; setActive?: boolean }) {
    return http.post<{ account: DoubaoAccountView }>("/doubao-accounts", input);
}

export async function bulkImportDoubaoAccounts(input: { text: string; site?: PoolSite; tags?: string[]; setActive?: boolean }) {
    return http.post<{ result: DoubaoBulkImportResult; status: DoubaoPoolStatus }>("/doubao-accounts/bulk-import", input);
}

export async function updateDoubaoAccount(id: string, patch: { label?: string; note?: string; tags?: string[]; enabled?: boolean }) {
    return http.patch<{ account: DoubaoAccountView }>(`/doubao-accounts/${id}`, patch);
}

export async function removeDoubaoAccount(id: string) {
    return http.delete<{ ok: boolean }>(`/doubao-accounts/${id}`);
}

export type DoubaoBatchAction = "activate" | "remove" | "clear-cooldown" | "enable" | "disable" | "tag";

export async function batchDoubaoAccounts(action: DoubaoBatchAction, ids: string[], tags?: string[], site?: PoolSite) {
    return http.post<{ affected: number; status: DoubaoPoolStatus }>("/doubao-accounts/batch", { action, ids, tags, site });
}

export async function clearAllDoubaoCooldowns() {
    return http.post<{ cleared: number }>("/doubao-accounts/clear-cooldowns");
}

/** 手动冷却（复用 mark-failed 的冷却机制，kind=rate_limited + 自定义时长）。 */
export async function cooldownDoubaoAccount(id: string, cooldownMs: number) {
    return http.post<{ switched: boolean }>(`/doubao-accounts/${id}/mark-failed`, { kind: "rate_limited", message: "手动冷却", cooldownMs });
}

/** 生成结果回报（文生图 / 文生视频调用方使用）。 */
export async function markDoubaoSuccess(id: string) {
    return http.post<{ ok: boolean }>(`/doubao-accounts/${id}/mark-success`);
}

export async function markDoubaoFailed(id: string, kind: "rate_limited" | "quota_exhausted" | "session_expired", message: string) {
    return http.post<{ switched: boolean }>(`/doubao-accounts/${id}/mark-failed`, { kind, message });
}

/** 豆包文生图（走账号池）。超时给足 6 分钟。 */
export async function generateDoubaoImage(input: { prompt: string; model?: string; ratio?: string; style?: string }, timeoutMs = 360_000) {
    return http.post<{ urls: string[]; account: string; text?: string }>("/doubao/generate/image", input, { timeout: timeoutMs });
}

/** 豆包/Dola 文生视频（走账号池，同步等待出片，最长约 12 分钟）。
 *  site 为站点偏好：dola- 前缀模型传 "dola"（只用 Dola 账号），空 = 豆包优先。 */
export async function generateDoubaoVideo(input: { prompt: string; model?: string; duration?: number; ratio?: string; site?: string }, timeoutMs = 900_000) {
    return http.post<{ urls: string[]; account: string; message?: string }>("/doubao/generate/video", input, { timeout: timeoutMs });
}

// ------------------------------------------------------------ 扫码登录

export type DoubaoQrState = "idle" | "waiting" | "success" | "expired" | "canceled" | "failed";

export type DoubaoQrSession = {
    state: DoubaoQrState;
    message: string;
    accountId?: string;
    masked?: string;
    hasBrowser: boolean;
    elapsedText: string;
};

/** 启动扫码登录（后端会弹出本机浏览器打开对应站点登录页）。已在进行中时返回现有会话。 */
export async function startDoubaoQrLogin(site: PoolSite = "doubao") {
    return http.post<{ session: DoubaoQrSession; started: boolean }>("/doubao-accounts/qr/start", { site });
}

export async function fetchDoubaoQrStatus(site: PoolSite = "doubao") {
    return http.get<{ session: DoubaoQrSession }>("/doubao-accounts/qr/status", { params: { site } });
}

export async function cancelDoubaoQrLogin(site: PoolSite = "doubao") {
    return http.post<{ canceled: boolean }>("/doubao-accounts/qr/cancel", { site });
}

// ------------------------------------------------------------ 手动过安全验证（710022004 风控）

export type DoubaoVerifyState = "idle" | "waiting" | "success" | "expired" | "canceled" | "failed";

export type DoubaoVerifySession = {
    state: DoubaoVerifyState;
    message: string;
    accountId?: string;
    accountLabel?: string;
    site?: PoolSite;
    hasBrowser: boolean;
    elapsedText: string;
};

/** 为指定账号启动验证窗口：后端弹出本机浏览器并预注入该账号 Cookie，用户过滑块后回收 Cookie。已有会话时返回现有会话。 */
export async function startDoubaoVerify(id: string) {
    return http.post<{ session: DoubaoVerifySession; started: boolean }>(`/doubao-accounts/${id}/verify/start`);
}

export async function fetchDoubaoVerifyStatus() {
    return http.get<{ session: DoubaoVerifySession }>("/doubao-accounts/verify/status");
}

/** 用户在窗口中完成滑块/安全验证后调用：回收最新完整 Cookie 并恢复账号可用。 */
export async function captureDoubaoVerify() {
    return http.post<{ session: DoubaoVerifySession }>("/doubao-accounts/verify/capture");
}

export async function cancelDoubaoVerify() {
    return http.post<{ canceled: boolean }>("/doubao-accounts/verify/cancel");
}

// ------------------------------------------------------------ 补抓浏览器指纹

/** 补抓指定账号的浏览器指纹（同步执行，约 15~40 秒，期间会短暂弹出浏览器窗口）。 */
export async function refreshDoubaoFingerprint(id: string) {
    return http.post<{ account: DoubaoAccountView }>(`/doubao-accounts/${id}/fingerprint/refresh`, undefined, { timeout: 120_000 });
}

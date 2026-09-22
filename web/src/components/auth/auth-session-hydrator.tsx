import type { ReactNode } from "react";
import { useEffect } from "react";

import { getAuthSession, type AuthSessionPayload } from "@/services/api/auth";
import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { preloadWorkspaceRoute } from "@/lib/workspace-route-modules";
import { useUserStore } from "@/stores/use-user-store";

// start.bat / 桌面版冷启动时后端可能还在编译或迁移，首屏会话请求会失败。
// 按退避间隔重试（合计约 25 秒）覆盖冷启动窗口；全部失败才按匿名会话放行，
// 否则本地单机版会被误跳到登录页并停留。
const SESSION_RETRY_DELAYS_MS = [1500, 3000, 5000, 8000, 8000];

export function AuthSessionHydrator({ children }: { children: ReactNode }) {
    const hydrated = useUserStore((state) => state.hydrated);

    useEffect(() => {
        let cancelled = false;
        const hydrate = async (attempt = 0): Promise<void> => {
            let payload: AuthSessionPayload;
            try {
                payload = await getAuthSession();
            } catch {
                const delay = SESSION_RETRY_DELAYS_MS[attempt];
                if (delay === undefined) {
                    if (!cancelled) applyAnonymousSession({ user: null, logicalModels: [] });
                    return;
                }
                await new Promise((resolve) => setTimeout(resolve, delay));
                if (!cancelled) await hydrate(attempt + 1);
                return;
            }
            if (cancelled) return;
            if (!payload.user) {
                applyAnonymousSession(payload);
                return;
            }
            // 账号数据、画布和素材持久化只属于已登录工作区，登录页不下载这些模块。
            const { applyUserSession } = await import("@/lib/user-session");
            if (cancelled) return;
            await applyUserSession(payload);
            preloadWorkspaceRoute(window.location.pathname);
        };
        void hydrate();
        return () => {
            cancelled = true;
        };
    }, []);

    return hydrated ? children : <FullScreenLoader />;
}

function applyAnonymousSession(payload: AuthSessionPayload) {
    const store = useUserStore.getState();
    store.clearSession();
    store.setLocalMode(Boolean(payload.localMode));
    store.setRuntimeLimits(payload.runtimeLimits);
    store.setDrawingEngine(payload.drawingEngine);
    store.setFeatures(payload.features);
    store.setHydrated(true);
}

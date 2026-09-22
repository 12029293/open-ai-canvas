import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router";

import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { useUserStore } from "@/stores/use-user-store";

export function RequireAuth({ children }: { children: ReactNode }) {
    const location = useLocation();
    const hydrated = useUserStore((state) => state.hydrated);
    const user = useUserStore((state) => state.user);
    const localMode = useUserStore((state) => state.localMode);

    if (!hydrated) return <FullScreenLoader />;
    // 本地单机模式没有登录页；会话短暂不可达时原地等待恢复，而不是跳登录。
    if (!user && localMode) return <FullScreenLoader label="正在连接本地服务" detail="本地服务暂未就绪，将自动恢复" />;
    if (!user) return <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />;
    return children;
}

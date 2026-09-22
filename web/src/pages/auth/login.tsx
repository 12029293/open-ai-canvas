import { type FormEvent, useEffect, useState, type ReactNode } from "react";
import { App, Button, Divider, Input } from "antd";
import { ArrowRight, LockKeyhole, UserRound } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router";

import { getAuthSession, getAuthSettings, linuxDOLoginURL, login } from "@/services/api/auth";
import { useUserStore } from "@/stores/use-user-store";
import { LinuxDOIcon } from "./auth-scene";

// 后端冷启动时该请求会失败：按退避间隔重试直到拿到配置（合计约 25 秒）。
const AUTH_SETTINGS_RETRY_DELAYS_MS = [1500, 3000, 5000, 8000, 8000];

export default function LoginPage() {
    const navigate = useNavigate();
    const [params] = useSearchParams();
    const { message } = App.useApp();
    const [username, setUsername] = useState("");
    const [password, setPassword] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [linuxdoEnabled, setLinuxdoEnabled] = useState(false);
    const [localMode, setLocalMode] = useState(useUserStore.getState().localMode);
    const next = safeNext(params.get("next"));
    const forgotPasswordURL = `/forgot-password?next=${encodeURIComponent(next)}`;
    const user = useUserStore((state) => state.user);
    const hydrated = useUserStore((state) => state.hydrated);

    // 如果已登录，直接跳转
    useEffect(() => {
        if (hydrated && user) {
            navigate(next, { replace: true });
        }
    }, [hydrated, user, next, navigate]);

    // 后端冷启动时该请求会失败：按退避间隔重试直到拿到配置。
    // 若首次失败即放弃，localMode 永远不会被置真，
    // 本地单机版的“自动进入工作台”轮询也不会被激活，页面会停在登录表单。
    useEffect(() => {
        let cancelled = false;
        const load = async (attempt = 0): Promise<void> => {
            try {
                const settings = await getAuthSettings();
                if (cancelled) return;
                setLinuxdoEnabled(settings.linuxdoEnabled);
                if (settings.localMode) {
                    setLocalMode(true);
                    useUserStore.getState().setLocalMode(true);
                }
            } catch (error) {
                const delay = AUTH_SETTINGS_RETRY_DELAYS_MS[attempt];
                if (delay === undefined) {
                    // 这是登录页的展示配置读取：失败时明确隐藏第三方入口，
                    // 账号密码登录仍可用；不能无痕地把配置读取失败当成成功。
                    console.warn("读取登录方式配置失败，已隐藏第三方登录入口", error);
                    return;
                }
                await new Promise((resolve) => setTimeout(resolve, delay));
                if (!cancelled) await load(attempt + 1);
            }
        };
        void load();
        const oauthError = params.get("oauth_error");
        if (oauthError) message.error(oauthError);
        return () => {
            cancelled = true;
        };
    }, [message, params]);

    // 本地单机模式：轮询会话，本地服务恢复后自动进入工作台。
    useEffect(() => {
        if (!localMode) return;
        let cancelled = false;
        const poll = async () => {
            try {
                const payload = await getAuthSession();
                if (cancelled || !payload.user) return;
                const { applyUserSession } = await import("@/lib/user-session");
                if (cancelled) return;
                await applyUserSession(payload);
            } catch {
                // 本地服务未就绪，下一轮继续。
            }
        };
        void poll();
        const timer = window.setInterval(() => void poll(), 2000);
        return () => {
            cancelled = true;
            window.clearInterval(timer);
        };
    }, [localMode]);

    if (localMode) {
        return (
            <div className="flex flex-col items-center gap-3 py-16 text-center" aria-live="polite">
                <div className="size-8 animate-spin rounded-full border-2 border-white/20 border-t-white/70" />
                <p className="text-sm font-medium text-white/80">正在进入影策工作台</p>
                <p className="text-xs text-white/45">本地服务暂未就绪，恢复后将自动进入，无需登录</p>
            </div>
        );
    }

    const submit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setSubmitting(true);
        try {
            await login({ username, password });
            const { applyUserSession } = await import("@/lib/user-session");
            await applyUserSession(await getAuthSession());
            message.success("登录成功");
            navigate(next, { replace: true });
        } catch (error) {
            message.error(error instanceof Error ? error.message : "登录失败");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <form onSubmit={submit} className="space-y-5">
            <AuthField label="用户名 / 邮箱" htmlFor="login-account">
                <Input id="login-account" size="large" prefix={<UserRound className="size-4 text-white/35" />} value={username} onChange={(event) => setUsername(event.target.value)} placeholder="用户名或邮箱" autoComplete="username" required />
            </AuthField>
            <AuthField
                label="密码"
                htmlFor="login-password"
                action={
                    <Link
                        to={forgotPasswordURL}
                        className="-my-2 inline-flex min-h-8 items-center rounded-sm text-xs font-medium text-blue-300/80 transition-colors hover:text-blue-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-300/45"
                    >
                        忘记密码？
                    </Link>
                }
            >
                <Input.Password
                    id="login-password"
                    size="large"
                    prefix={<LockKeyhole className="size-4 text-white/35" />}
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    placeholder="请输入密码"
                    autoComplete="current-password"
                    required
                />
            </AuthField>
            <Button type="primary" htmlType="submit" size="large" block loading={submitting} icon={<ArrowRight className="size-4" />} iconPlacement="end">
                登录
            </Button>
            {linuxdoEnabled ? (
                <>
                    <Divider plain className="!border-white/10 !text-white/30">
                        或
                    </Divider>
                    <Button size="large" block icon={<LinuxDOIcon />} href={linuxDOLoginURL(next)}>
                        使用 Linux.do 登录
                    </Button>
                </>
            ) : null}
        </form>
    );
}

function AuthField({ label, htmlFor, action, children }: { label: string; htmlFor: string; action?: ReactNode; children: ReactNode }) {
    return (
        <div className="space-y-2">
            <div className="flex items-center justify-between gap-3">
                <label htmlFor={htmlFor} className="text-xs font-medium text-white/62">
                    {label}
                </label>
                {action}
            </div>
            {children}
        </div>
    );
}

function safeNext(value: string | null) {
    if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
    return value;
}

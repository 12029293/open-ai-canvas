export const WORKSPACE_SIDEBAR_STORAGE_KEY = "infinite-canvas:workspace-sidebar-collapsed";
export const WORKSPACE_SIDEBAR_CHANGE_EVENT = "workspace:sidebar-collapsed-change";
export const ADMIN_SIDEBAR_STORAGE_KEY = "infinite-canvas:admin-sidebar-collapsed";
export const ADMIN_SIDEBAR_CHANGE_EVENT = "admin:sidebar-collapsed-change";

type WorkspaceSidebarStorage = Pick<Storage, "getItem" | "setItem">;

type SidebarCollapsedController = {
    read: () => boolean;
    write: (collapsed: boolean) => void;
    publish: (collapsed: boolean) => void;
    subscribe: (onChange: (collapsed: boolean) => void) => () => void;
};

// 工作台与管理后台的侧栏折叠状态各自独立，避免一边展开/收起串到另一边。
// defaultCollapsed 是 localStorage 无记录时（首次打开）的初始状态；用户切换过一次后以存储值为准。
function createSidebarCollapsedController(storageKey: string, changeEvent: string, defaultCollapsed: boolean): SidebarCollapsedController {
    const storage = () => {
        if (typeof window === "undefined") return undefined;
        try {
            return window.localStorage;
        } catch {
            return undefined;
        }
    };

    return {
        read() {
            try {
                const stored = storage()?.getItem(storageKey);
                if (stored === "1") return true;
                if (stored === "0") return false;
                return defaultCollapsed;
            } catch {
                return defaultCollapsed;
            }
        },
        write(collapsed) {
            try {
                storage()?.setItem(storageKey, collapsed ? "1" : "0");
            } catch {
                // localStorage 不可用时保留当前内存状态，不能阻断侧栏交互。
            }
        },
        publish(collapsed) {
            this.write(collapsed);
            window.dispatchEvent(new CustomEvent(changeEvent, { detail: { collapsed } }));
        },
        subscribe(onChange) {
            const handleChange = (event: Event) => onChange(Boolean((event as CustomEvent<{ collapsed?: boolean }>).detail?.collapsed));
            const handleStorage = (event: StorageEvent) => {
                if (event.key === storageKey) onChange(event.newValue === "1");
            };
            window.addEventListener(changeEvent, handleChange);
            window.addEventListener("storage", handleStorage);
            return () => {
                window.removeEventListener(changeEvent, handleChange);
                window.removeEventListener("storage", handleStorage);
            };
        },
    };
}

// 工作台首次打开默认收起（窄轨），管理后台维持默认展开。
const workspaceSidebarState = createSidebarCollapsedController(WORKSPACE_SIDEBAR_STORAGE_KEY, WORKSPACE_SIDEBAR_CHANGE_EVENT, true);
const adminSidebarState = createSidebarCollapsedController(ADMIN_SIDEBAR_STORAGE_KEY, ADMIN_SIDEBAR_CHANGE_EVENT, false);

export function readWorkspaceSidebarCollapsed() {
    return workspaceSidebarState.read();
}

export function writeWorkspaceSidebarCollapsed(collapsed: boolean) {
    workspaceSidebarState.write(collapsed);
}

export function subscribeWorkspaceSidebarCollapsed(onChange: (collapsed: boolean) => void) {
    return workspaceSidebarState.subscribe(onChange);
}

export function readAdminSidebarCollapsed() {
    return adminSidebarState.read();
}

export function publishAdminSidebarCollapsed(collapsed: boolean) {
    adminSidebarState.publish(collapsed);
}

export function subscribeAdminSidebarCollapsed(onChange: (collapsed: boolean) => void) {
    return adminSidebarState.subscribe(onChange);
}

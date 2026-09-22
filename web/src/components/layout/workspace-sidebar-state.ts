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
function createSidebarCollapsedController(storageKey: string, changeEvent: string): SidebarCollapsedController {
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
                return storage()?.getItem(storageKey) === "1";
            } catch {
                return false;
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

const workspaceSidebarState = createSidebarCollapsedController(WORKSPACE_SIDEBAR_STORAGE_KEY, WORKSPACE_SIDEBAR_CHANGE_EVENT);
const adminSidebarState = createSidebarCollapsedController(ADMIN_SIDEBAR_STORAGE_KEY, ADMIN_SIDEBAR_CHANGE_EVENT);

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

import { WorkspaceTopBarExtensionSlot } from "@/components/layout/workspace-top-bar-extension";

export function WorkspaceTopBar() {
    return (
        <header className="app-workspace-topbar">
            <WorkspaceTopBarExtensionSlot />
        </header>
    );
}

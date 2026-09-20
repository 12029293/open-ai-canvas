import { App, Button, Input, InputNumber, Popconfirm, Select, Table } from "antd";
import { Globe, Plus, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import { AppModal } from "@/components/ui/product/app-modal/app-modal";

import { fetchDoubaoPoolStatus, POOL_SITE_META, type DoubaoAccountView, type PoolSite } from "@/services/api/doubao-accounts";
import {
    assignNetworkProxy,
    createNetworkProxy,
    deleteNetworkProxy,
    fetchNetworkProxies,
    testNetworkProxy,
    updateNetworkProxy,
    type NetworkProxyTestResult,
    type NetworkProxyUpsertRequest,
    type NetworkProxyView,
    type ProxyPoolType,
} from "@/services/api/network-proxies";
import { getWebRelayPoolStatus, type WebRelayPoolAccount } from "@/services/api/webrelay-accounts";

/**
 * 网络代理设置面板：代理列表（增删改查 + 测 IP）+ 账号分配。
 * 仅服务端直连 HTTP 链路生效（豆包/Dola 网页接口、DeepSeek 网页版）；
 * 千问 / Dola 浏览器中继链路不受代理影响，界面上会标注。
 */

type ProxyFormState = {
    id?: string;
    name: string;
    protocol: NetworkProxyView["protocol"];
    host: string;
    port: number;
    username: string;
    password: string;
    hasPassword: boolean;
};

const emptyForm: ProxyFormState = { name: "", protocol: "http", host: "", port: 8001, username: "", password: "", hasPassword: false };

const WEBRELAY_SITE_LABEL: Record<string, string> = { deepseek: "DeepSeek", qwen: "千问（浏览器链路）" };

type AssignRow = {
    key: string;
    poolType: ProxyPoolType;
    site: string;
    siteLabel: string;
    label: string;
    state: string;
    statusText: string;
    proxyId: string;
    browserRelay: boolean;
};

export function NetworkProxySettingsPane() {
    const { message } = App.useApp();
    const [proxies, setProxies] = useState<NetworkProxyView[]>([]);
    const [rows, setRows] = useState<AssignRow[]>([]);
    const [loading, setLoading] = useState(false);
    const [testing, setTesting] = useState<string | null>(null);
    const [testResults, setTestResults] = useState<Record<string, NetworkProxyTestResult>>({});
    const [formOpen, setFormOpen] = useState(false);
    const [form, setForm] = useState<ProxyFormState>(emptyForm);
    const [saving, setSaving] = useState(false);
    const [selectedKeys, setSelectedKeys] = useState<React.Key[]>([]);
    const [batchProxyId, setBatchProxyId] = useState<string>("");
    const [assigning, setAssigning] = useState(false);

    const proxyName = useCallback((id: string) => proxies.find((proxy) => proxy.id === id)?.name ?? "", [proxies]);

    const reload = useCallback(async () => {
        setLoading(true);
        try {
            const [proxyResult, doubaoResult, deepseekResult, qwenResult] = await Promise.all([
                fetchNetworkProxies(),
                fetchDoubaoPoolStatus().catch(() => null),
                getWebRelayPoolStatus("deepseek").catch(() => null),
                getWebRelayPoolStatus("qwen").catch(() => null),
            ]);
            setProxies(proxyResult.proxies ?? []);
            const nextRows: AssignRow[] = [];
            const doubaoStatus = doubaoResult as { accounts?: DoubaoAccountView[] } | null;
            for (const account of doubaoStatus?.accounts ?? []) {
                nextRows.push({
                    key: `doubao:${account.id}`,
                    poolType: "doubao",
                    site: account.site,
                    siteLabel: POOL_SITE_META[account.site as PoolSite]?.label ?? account.site,
                    label: account.label || account.masked,
                    state: account.state,
                    statusText: account.statusText,
                    proxyId: account.proxyId ?? "",
                    browserRelay: false,
                });
            }
            for (const [site, status] of [["deepseek", deepseekResult], ["qwen", qwenResult]] as const) {
                const poolStatus = status as { accounts?: WebRelayPoolAccount[] } | null;
                for (const account of poolStatus?.accounts ?? []) {
                    nextRows.push({
                        key: `webrelay:${account.id}`,
                        poolType: "webrelay",
                        site,
                        siteLabel: WEBRELAY_SITE_LABEL[site] ?? site,
                        label: account.label || account.masked,
                        state: account.state,
                        statusText: account.statusText,
                        proxyId: account.proxyId ?? "",
                        browserRelay: site === "qwen",
                    });
                }
            }
            setRows(nextRows);
        } catch (error) {
            message.error(error instanceof Error ? `加载网络代理配置失败：${error.message}` : "加载网络代理配置失败");
        } finally {
            setLoading(false);
        }
    }, [message]);

    useEffect(() => {
        void reload();
    }, [reload]);

    const openCreate = () => {
        setForm({ ...emptyForm });
        setFormOpen(true);
    };

    const openEdit = (proxy: NetworkProxyView) => {
        setForm({ id: proxy.id, name: proxy.name, protocol: proxy.protocol, host: proxy.host, port: proxy.port, username: proxy.username ?? "", password: "", hasPassword: proxy.hasPassword });
        setFormOpen(true);
    };

    const saveForm = async () => {
        if (!form.name.trim()) { message.warning("请填写代理名称"); return; }
        if (!form.host.trim()) { message.warning("请填写主机地址"); return; }
        if (!form.port || form.port < 1 || form.port > 65535) { message.warning("端口需在 1-65535 之间"); return; }
        setSaving(true);
        try {
            const input: NetworkProxyUpsertRequest = { name: form.name.trim(), protocol: form.protocol, host: form.host.trim(), port: form.port, username: form.username.trim(), remark: "" };
            if (form.password.trim()) {
                input.password = form.password.trim();
            } else if (form.id && form.hasPassword === false) {
                input.clearPassword = true;
            }
            if (form.id) {
                await updateNetworkProxy(form.id, input);
                message.success("代理已更新");
            } else {
                await createNetworkProxy(input);
                message.success("代理已添加");
            }
            setFormOpen(false);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存代理失败");
        } finally {
            setSaving(false);
        }
    };

    const runTest = async (proxy: NetworkProxyView) => {
        setTesting(proxy.id);
        try {
            const { result } = await testNetworkProxy(proxy.id);
            setTestResults((current) => ({ ...current, [proxy.id]: result }));
            if (result.ok) {
                message.success(`${proxy.name}：连接正常（${result.latencyMs ?? 0}ms）`);
            } else {
                message.error(`${proxy.name}：${result.message || "连接失败"}`);
            }
        } catch (error) {
            message.error(error instanceof Error ? error.message : "测试失败");
        } finally {
            setTesting(null);
        }
    };

    const removeProxy = async (proxy: NetworkProxyView) => {
        try {
            await deleteNetworkProxy(proxy.id);
            message.success("代理已删除，相关账号已恢复直连");
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "删除代理失败");
        }
    };

    const applyAssign = async (poolType: ProxyPoolType, ids: string[], proxyId: string) => {
        if (ids.length === 0) return;
        setAssigning(true);
        try {
            await assignNetworkProxy(poolType, ids, proxyId);
            message.success(proxyId ? `已应用到 ${ids.length} 个账号：${proxyName(proxyId)}` : `已恢复 ${ids.length} 个账号为直连`);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "代理分配失败");
        } finally {
            setAssigning(false);
        }
    };

    const applyBatch = async () => {
        const grouped = new Map<ProxyPoolType, string[]>();
        for (const key of selectedKeys) {
            const row = rows.find((item) => item.key === key);
            if (!row || row.browserRelay) continue;
            grouped.set(row.poolType, [...(grouped.get(row.poolType) ?? []), key.split(":")[1] ?? row.key]);
        }
        let applied = 0;
        for (const [poolType, ids] of grouped) {
            if (ids.length === 0) continue;
            await assignNetworkProxy(poolType, ids, batchProxyId);
            applied += ids.length;
        }
        if (applied === 0) {
            message.warning("请先勾选支持代理的账号");
            return;
        }
        message.success(batchProxyId ? `已应用到 ${applied} 个账号：${proxyName(batchProxyId)}` : `已恢复 ${applied} 个账号为直连`);
        setSelectedKeys([]);
        await reload();
    };

    const proxyOptions = useMemo(() => [
        { value: "", label: "直连（不走代理）" },
        ...proxies.map((proxy) => ({ value: proxy.id, label: `${proxy.name}（${proxy.protocol}://${proxy.host}:${proxy.port}）` })),
    ], [proxies]);

    const renderProxySelect = (row: AssignRow) => {
        if (row.browserRelay) {
            return <span className="text-xs text-muted-foreground">浏览器链路，暂不支持代理</span>;
        }
        return (
            <Select
                size="small"
                className="w-56"
                value={row.proxyId}
                options={proxyOptions}
                disabled={assigning}
                onChange={(value) => void applyAssign(row.poolType, [row.key.split(":")[1] ?? row.key], value)}
            />
        );
    };

    return (
        <div className="flex flex-col gap-6">
            <section className="settings-preference-block">
                <div className="settings-preference-heading">
                    <h3 className="flex items-center gap-2"><Globe className="size-4" /> 代理列表</h3>
                    <p>支持 HTTP / HTTPS / SOCKS5。删除代理会自动解绑所有账号（回到直连）。</p>
                </div>
                <div className="flex justify-end gap-2 pb-2">
                    <Button icon={<RefreshCw className="size-4" />} onClick={() => void reload()} loading={loading}>刷新</Button>
                    <Button type="primary" icon={<Plus className="size-4" />} onClick={openCreate}>新增代理</Button>
                </div>
                <Table
                    size="small"
                    rowKey="id"
                    loading={loading}
                    dataSource={proxies}
                    pagination={false}
                    locale={{ emptyText: "还没有代理，点击「新增代理」添加" }}
                    columns={[
                        { title: "名称", dataIndex: "name", width: 180 },
                        {
                            title: "地址",
                            key: "address",
                            render: (_: unknown, proxy: NetworkProxyView) => (
                                <code className="text-xs">{proxy.protocol}://{proxy.host}:{proxy.port}{proxy.username ? "（需认证）" : ""}</code>
                            ),
                        },
                        {
                            title: "连通性",
                            key: "test",
                            width: 220,
                            render: (_: unknown, proxy: NetworkProxyView) => {
                                const result = testResults[proxy.id];
                                if (!result) return <span className="text-xs text-muted-foreground">未测试</span>;
                                return result.ok
                                    ? <span className="text-xs">正常 · {result.latencyMs}ms</span>
                                    : <span className="text-xs text-red-500">{result.message || "失败"}</span>;
                            },
                        },
                        {
                            title: "操作",
                            key: "actions",
                            width: 230,
                            render: (_: unknown, proxy: NetworkProxyView) => (
                                <div className="flex gap-2">
                                    <Button size="small" loading={testing === proxy.id} onClick={() => void runTest(proxy)}>测IP</Button>
                                    <Button size="small" onClick={() => openEdit(proxy)}>编辑</Button>
                                    <Popconfirm title="删除该代理？绑定它的账号会恢复直连。" onConfirm={() => void removeProxy(proxy)}>
                                        <Button size="small" danger>删除</Button>
                                    </Popconfirm>
                                </div>
                            ),
                        },
                    ]}
                />
            </section>

            <section className="settings-preference-block">
                <div className="settings-preference-heading">
                    <h3>账号分配</h3>
                    <p>豆包 / Dola / DeepSeek 走服务端直连请求，绑定后该账号的生成请求经过指定代理；千问、Dola 网页登录属于浏览器链路，暂不生效。</p>
                </div>
                {selectedKeys.length > 0 ? (
                    <div className="flex flex-wrap items-center gap-2 pb-2">
                        <span className="text-sm text-muted-foreground">已选 {selectedKeys.length} 个账号，设置为</span>
                        <Select className="w-64" value={batchProxyId} options={proxyOptions} onChange={setBatchProxyId} />
                        <Button type="primary" size="small" loading={assigning} onClick={() => void applyBatch()}>应用到所选</Button>
                        <Button size="small" onClick={() => setSelectedKeys([])}>取消选择</Button>
                    </div>
                ) : null}
                <Table
                    size="small"
                    rowKey="key"
                    loading={loading}
                    dataSource={rows}
                    pagination={rows.length > 20 ? { pageSize: 20 } : false}
                    rowSelection={{ selectedRowKeys: selectedKeys, onChange: (keys) => setSelectedKeys(keys), getCheckboxProps: (row) => ({ disabled: row.browserRelay }) }}
                    locale={{ emptyText: "账号池为空，先到「账号池」页导入账号" }}
                    columns={[
                        { title: "平台", dataIndex: "siteLabel", width: 130 },
                        { title: "账号", dataIndex: "label", ellipsis: true },
                        { title: "状态", dataIndex: "statusText", width: 150, render: (text: string) => <span className="text-xs text-muted-foreground">{text}</span> },
                        { title: "代理", key: "proxy", width: 250, render: (_: unknown, row: AssignRow) => renderProxySelect(row) },
                    ]}
                />
            </section>

            <AppModal
                title={form.id ? "编辑代理" : "新增代理"}
                open={formOpen}
                onOk={() => void saveForm()}
                confirmLoading={saving}
                onCancel={() => setFormOpen(false)}
                okText="保存"
                cancelText="取消"
            >
                <div className="flex flex-col gap-3 pt-2">
                    <label className="flex flex-col gap-1 text-sm">
                        名称
                        <Input value={form.name} placeholder="方便自己辨认，如：家里 Clash" onChange={(event) => setForm({ ...form, name: event.target.value })} />
                    </label>
                    <div className="grid grid-cols-[120px_1fr_120px] gap-3">
                        <label className="flex flex-col gap-1 text-sm">
                            协议
                            <Select value={form.protocol} options={[{ value: "http", label: "HTTP" }, { value: "https", label: "HTTPS" }, { value: "socks5", label: "SOCKS5" }]} onChange={(value) => setForm({ ...form, protocol: value })} />
                        </label>
                        <label className="flex flex-col gap-1 text-sm">
                            主机
                            <Input value={form.host} placeholder="如 direct.example.com 或 127.0.0.1" onChange={(event) => setForm({ ...form, host: event.target.value })} />
                        </label>
                        <label className="flex flex-col gap-1 text-sm">
                            端口
                            <InputNumber className="w-full" min={1} max={65535} precision={0} value={form.port} onChange={(value) => setForm({ ...form, port: Number(value) || 0 })} />
                        </label>
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                        <label className="flex flex-col gap-1 text-sm">
                            用户名（可选）
                            <Input value={form.username} autoComplete="off" onChange={(event) => setForm({ ...form, username: event.target.value })} />
                        </label>
                        <label className="flex flex-col gap-1 text-sm">
                            密码（可选）
                            <Input.Password value={form.password} autoComplete="new-password" onChange={(event) => setForm({ ...form, password: event.target.value })} />
                        </label>
                    </div>
                </div>
            </AppModal>
        </div>
    );
}

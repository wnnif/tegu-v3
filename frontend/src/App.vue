<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue';
import { customerMoney } from './customerPrices';
import releaseVersion from '../../VERSION?raw';
const appVersion = `v${releaseVersion.trim().replace(/^v/, '')}`;
import { api, command, apiSession, onSessionExpired, onSessionValidate, SessionChangedError } from './api';
import { canAccess, pageAllowed, type SessionUser } from './sessionAccess';
import UserEditor from './components/UserEditor.vue';
import EntityEditor from './components/EntityEditor.vue';
import WarehouseCatalog from './components/WarehouseCatalog.vue';
import HistoryPanel from './components/HistoryPanel.vue';
import WageFinancePanel from './components/WageFinancePanel.vue';
import ExpenseManagementPanel from './components/ExpenseManagementPanel.vue';
import FinanceSummaryPanel from './FinanceSummaryPanel.vue';
import ReceiptManagementPanel from './components/ReceiptManagementPanel.vue';
import TicketIssuePanel from './components/TicketIssuePanel.vue';
import OrderPrint from './components/OrderPrint.vue';
import PrintSettings from './components/PrintSettings.vue';
import InventoryPanel from './components/InventoryPanel.vue';
import AccountPanel from './components/AccountPanel.vue';
import DashboardPanel from './components/DashboardPanel.vue';
import StockEditor from './components/StockEditor.vue';
import ArchivePanel from './components/ArchivePanel.vue';
import BackupConsole from './components/BackupConsole.vue';
import OrderEditor from './components/OrderEditor.vue';
import ProjectsPanel from './components/ProjectsPanel.vue';
import ReferenceSelect from './components/ReferenceSelect.vue';
import { useCatalog } from './workflows/useCatalog';
import { useOrders } from './workflows/useOrders';
import { useStock } from './workflows/useStock';
import { useMaintenance, emptyBackupConfig } from './workflows/useMaintenance';
import { referenceById } from './workflows/references';
import type { FinanceDashboardSourceNavigation } from './financeDashboard';
import type { LoginUser, References, Balance, CatalogRow, UserRow, OrderRow, BackupConfig, Entity, WorkflowContext, PageResult } from './workflows/models';
import type { PrintOrder } from './printOrder';
import { sourceOrderId } from './settlements';
const emptyReferences = () => ({ companies: [], workers: [], products: [], warehouses: [], crafts: [] });
const user = ref<LoginUser | null>(null), categories = ref<string[]>([]), page = ref('dashboard'), error = ref(''), notice = ref(''), busy = ref(false), loading = ref(false), dialog = ref(''), catalogRows = ref<CatalogRow[]>([]), userRows = ref<UserRow[]>([]), orderRows = ref<OrderRow[]>([]), references = ref<References>(emptyReferences()), inventory = ref<Balance[]>([]), login = reactive({ username: '', password: '' });
const month = ref(new Date().toLocaleDateString('sv-SE').slice(0, 7)), company = ref(''), category = ref('全部'), search = ref(''), status = ref(''), catalogStatus = ref('1'), workerFilter = ref(''), pageNo = ref(1), total = ref(0), totalAmount = ref<number | undefined>(), selected = ref<string[]>([]), showZero = ref(true), inventoryView = ref('stock'), viewGeneration = ref(0), accessRevision = ref(0);
const printData = ref<PrintOrder | null>(null), historyRevision = ref(0);
const wageFinancePanel = ref<InstanceType<typeof WageFinancePanel> | null>(null);
const projectsPanel = ref<InstanceType<typeof ProjectsPanel> | null>(null);
const ticketFinancePanel = ref<InstanceType<typeof ReceiptManagementPanel> | null>(null);
const receiptCompany = ref('');
const ticketIssuePanel = ref<InstanceType<typeof TicketIssuePanel> | null>(null);
const expenseManagementPanel = ref<InstanceType<typeof ExpenseManagementPanel> | null>(null);
const financeSummaryPanel = ref<InstanceType<typeof FinanceSummaryPanel> | null>(null);
const financeSourceScope = ref<FinanceDashboardSourceNavigation | null>(null), sourceDateFrom = ref(''), sourceDateTo = ref('');
const financeSummaryPermissions = computed(() => ['finance','customer_prices','wages','expenses','warehouse','orders'].filter(permission => can(permission)));
let submit = command();
let accessCheck: Promise<void> | undefined;
let alive = true;
let accessTimer: ReturnType<typeof setInterval> | undefined;
function accessFingerprint(account: LoginUser, availableCategories = categories.value) {
    return JSON.stringify([account.id, account.role, [...account.permissions].sort(), [...(account.crafts || [])].sort(), [...availableCategories].sort()]);
}
let knownAccessFingerprint = '';
const money = (n: unknown) => '¥ ' + (Number(n || 0) / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 }), customerAmount = (n: unknown) => customerMoney(n, can('customer_prices')), quantity = (n: unknown) => Number(n || 0) / 1e6;
const can = (p: string) => canAccess(user.value, p);
const menus = [{ name: '工作台', id: 'dashboard', permission: '' }, { name: '货物管理', children: [{ name: '商品管理', id: 'products', permission: 'warehouse' }, { name: '库存管理', id: 'inventory', permission: 'warehouse' }, { name: '仓库管理', id: 'warehouses', permission: 'warehouse' }] }, { name: '制单', children: [{ name: '客户与工地', id: 'companies', permission: 'customers' }, { name: '工地汇总', id: 'projects', permission: 'projects' }, { name: '施工制单', id: 'orders', permission: 'orders' }] }, { name: '财务', children: [{ name: '财务汇总', id: 'finance-summary', permission: 'finance' }, { name: '工资管理', id: 'wages', permission: 'wages' }, { name: '报销与费用预支', id: 'expenses', permission: 'expenses' }, { name: '收款管理', id: 'finance', permission: 'finance' }, { name: '交票管理', id: 'ticket-issue', permission: 'ticket_issue' }, { name: '月度表格归档', id: 'archives', permission: 'archive' }] }, { name: '系统管理', children: [{ name: '施工人管理', id: 'workers', permission: 'workers' }, { name: '工艺管理', id: 'crafts', permission: 'rates' }, { name: '员工权限', id: 'users', permission: 'users' }, { name: '操作日志', id: 'audit', permission: 'users' }, { name: '备份与维护', id: 'backups', permission: 'backups' }] }];
const menuAllowed = (p: string) => p === 'ticket_issue' ? can('finance') && can('customer_prices') : !p || p === 'archive' || p === 'projects' ? (p === 'archive' ? can('wages') || can('finance') || can('orders') || can('expenses') || can('warehouse') : p === 'projects' ? can('orders') || can('customers') || can('finance') : true) : can(p);
const title = computed(() => menus.flatMap(m => m.children || [m]).find(m => m.id === page.value)?.name || (page.value === 'workers' ? '施工人管理' : page.value === 'account' ? '我的账号' : '工作台'));
function clearView() {
    ++viewGeneration.value;
    loading.value = false;
    dialog.value = '';
    resetCatalog();
    resetOrders();
    resetStock();
    printData.value = null;
    catalogRows.value = [];
    userRows.value = [];
    orderRows.value = [];
    selected.value = [];
    inventory.value = [];
    total.value = 0;
    totalAmount.value = undefined;
    receiptCompany.value = '';
    backupConfig.value = emptyBackupConfig();
}
function clearSession(message = '') {
    knownAccessFingerprint = '';
    accessCheck = undefined;
    apiSession.invalidate();
    submit = command();
    clearView();
    user.value = null;
    categories.value = [];
    references.value = emptyReferences();
    page.value = 'dashboard';
    financeSourceScope.value = null;
    sourceDateFrom.value = '';
    sourceDateTo.value = '';
    company.value = '';
    search.value = '';
    status.value = '';
    catalogStatus.value = '1';
    workerFilter.value = '';
    pageNo.value = 1;
    category.value = '全部';
    craftCategory.value = '全部';
    selectedOrderCategory.value = '';
    month.value = new Date().toLocaleDateString('sv-SE').slice(0, 7);
    showZero.value = true;
    inventoryView.value = 'stock';
    historyRevision.value++;
    login.password = '';
    notice.value = '';
    error.value = message;
    busy.value = false;
}
const ctx: WorkflowContext = { dialog, error, notice, references, user, page, company, categories, category, inventory, perform, submit: (path, body) => submit(path, body), load, can };
const { catalogDraft, userDraft, craftCategory, companyCraftPage, companyCraftTotal, loadCompanyCraftOptions, searchCompanyCrafts, changeCompanyCraftCategory, editEntity, filterCatalog, catalogPage, toggleCatalog, saveEntity, companyCraftOptions, companyFixedVisible, hasCompanyCraft, toggleCompanyCraft, removeCompanyCraft, selectCompanyCategory, editUser, saveUser, deleteUser, resetCatalog } = useCatalog(ctx, { search, catalogStatus, pageNo, reload });
const { orderDraft, selectedOrderCategory, companyCraftCategories, workerOptions, allowedCrafts, projectOptions, projectPage, projectTotal, projectQuery, companyPackages, loadProjectOptions, onOrderCompanyChanged, projectChanged, continueProject, orderCategoryChanged, openOrder, orderLocked, orderWarehouse, workerChanged, lineWorkerChanged, categoryWorkerChanged, addLocation, removeLocation, materialProductSelected, addMaterial, addSale, addTemporary, saveOrder, cancelOrder, refreshOrderInventory, resetOrders } = useOrders(ctx, async (fresh) => projectsPanel.value?.refreshCurrent(fresh));
const { stockDraft, editStock, openStock, stockSourceChanged, saveStock, resetStock } = useStock(ctx);
const { backupConfig, saveBackupConfig, backup } = useMaintenance(perform, notice);
const stopSessionExpired = onSessionExpired(() => clearSession(user.value ? '登录已失效，请重新登录' : ''));
const stopSessionValidate = onSessionValidate(refreshSessionAccess);
async function perform(fn: () => Promise<void>) {
    if (busy.value)
        return;
    const revision = apiSession.capture();
    busy.value = true;
    error.value = '';
    try {
        await fn();
    }
    catch (e) {
        if (apiSession.current(revision) && !(e instanceof SessionChangedError))
            error.value = (e as Error).message;
    }
    finally {
        if (apiSession.current(revision))
            busy.value = false;
    }
}
async function init() {
    const revision = apiSession.capture();
    try {
        const r = await api<{
            user: LoginUser;
            categories: string[];
        }>('/me');
        apiSession.assert(revision);
        user.value = r.user;
        categories.value = r.categories;
        knownAccessFingerprint = accessFingerprint(r.user, r.categories);
        page.value = 'dashboard';
        await load();
    }
    catch (e) {
        if (apiSession.current(revision))
            clearSession(e instanceof SessionChangedError ? '' : (e as Error).message);
    }
}
function refreshSessionAccess(): Promise<void> {
    if (!user.value || !alive) return Promise.resolve();
    if (accessCheck) return accessCheck;
    const account = user.value, revision = apiSession.capture();
    const before = knownAccessFingerprint || accessFingerprint(account);
    const check = (async () => {
        const result = await api<{ user: LoginUser; categories: string[] }>('/me');
        apiSession.assert(revision);
        if (!alive) return;
        const after = accessFingerprint(result.user, result.categories);
        if (result.user.id !== account.id || after !== before) {
            apiSession.invalidate();
            submit = command();
            clearView();
            references.value = emptyReferences();
            categories.value = result.categories;
            user.value = result.user;
            knownAccessFingerprint = after;
            accessRevision.value++;
            historyRevision.value++;
            busy.value = false;
            pageNo.value = 1;
            notice.value = '账号权限已更新，旧页面和草稿已清除，请重新打开记录';
            if (!pageAllowed(result.user, page.value)) page.value = 'dashboard';
            // Do not await a read here: its preflight joins this same access check.
            void load().catch(() => {});
        } else {
            categories.value = result.categories;
            user.value = result.user;
            knownAccessFingerprint = after;
        }
    })().finally(() => { if (accessCheck === check) accessCheck = undefined; });
    accessCheck = check;
    return check;
}
async function signIn() {
    if (busy.value)
        return;
    const credentials = { ...login };
    clearSession();
    await perform(async () => {
        await api('/login', credentials);
        await init();
    });
}
async function signOut() {
    clearSession();
    await perform(async () => {
        await api('/logout', {});
    });
}
async function load() {
    if (!user.value)
        return;
    if (!pageAllowed(user.value, page.value)) {
        clearView();
        page.value = 'dashboard';
    }
    const generation = ++viewGeneration.value, revision = apiSession.capture(), target = page.value;
    const params = new URLSearchParams({ company_id: company.value, q: search.value, status: target === 'wages' ? '' : status.value, wage_paid: target === 'wages' ? status.value : '', worker_id: workerFilter.value, view: target, page: String(pageNo.value) });
    if (target === 'orders' && financeSourceScope.value?.view === 'orders' && financeSourceScope.value.scope === 'period') {
        params.set('from', sourceDateFrom.value);
        params.set('to', sourceDateTo.value);
    }
    else {
        params.set('month', month.value);
    }
    error.value = '';
    loading.value = true;
    catalogRows.value = [];
    userRows.value = [];
    orderRows.value = [];
    selected.value = [];
    total.value = 0;
    totalAmount.value = undefined;
    const current = () => apiSession.current(revision) && generation === viewGeneration.value && target === page.value && pageAllowed(user.value, target);
    try {
        if (target === 'backups') {
            const config = await api<BackupConfig>('/backup-settings');
            if (!current())
                return;
            backupConfig.value = config;
        }
        if (target === 'orders') {
            const result = await api<PageResult<OrderRow> & {
                total_cents?: number;
                wage_cents: number;
            }>('/orders?' + params);
            if (!current())
                return;
            orderRows.value = result.rows;
            total.value = result.total;
            totalAmount.value = result.total_cents;
        }
        else if (target === 'users') {
            const result = await api<PageResult<UserRow>>('/users?' + new URLSearchParams({ page: String(pageNo.value), q: search.value }));
            if (!current())
                return;
            userRows.value = result.rows;
            total.value = result.total;
        }
        else if (target === 'wages')
            await wageFinancePanel.value?.refresh();
        else if (target === 'finance')
            await ticketFinancePanel.value?.refresh();
        else if (target === 'inventory')
            historyRevision.value++;
        else if (['companies', 'workers', 'products', 'warehouses', 'crafts'].includes(target)) {
            const query = new URLSearchParams({ entity: target, page: String(pageNo.value), q: search.value });
            if (['products', 'warehouses', 'crafts'].includes(target) && catalogStatus.value)
                query.set('active', catalogStatus.value);
            if (target === 'crafts' && category.value !== '全部')
                query.set('category', category.value);
            const result = await api<PageResult<CatalogRow>>('/catalog?' + query);
            if (!current())
                return;
            catalogRows.value = result.rows;
            total.value = result.total;
        }
    }
    catch (e) {
        if (current() && !(e instanceof SessionChangedError)) {
            catalogRows.value = [];
            userRows.value = [];
            orderRows.value = [];
            selected.value = [];
            total.value = 0;
            totalAmount.value = undefined;
            if (target === 'backups')
                backupConfig.value = emptyBackupConfig();
            error.value = (e as Error).message;
        }
    }
    finally {
        if (current())
            loading.value = false;
        apiSession.assert(revision);
    }
}
function go(id: string, sourceScope?: FinanceDashboardSourceNavigation) {
    if (busy.value)
        return;
    if (!pageAllowed(user.value, id)) {
        error.value = '当前账号无此页面权限';
        return;
    }
    clearView();
    financeSourceScope.value = sourceScope || null;
    sourceDateFrom.value = sourceScope?.filters?.from || '';
    sourceDateTo.value = sourceScope?.filters?.to || '';
    page.value = id;
    pageNo.value = 1;
    search.value = '';
    status.value = '';
    catalogStatus.value = id === 'crafts' ? '' : '1';
    company.value = '';
    workerFilter.value = '';
    category.value = '全部';
    void load().catch(() => {
    });
}
function openReceipts(companyId: string) {
    if (!can('finance') || !can('customer_prices') || busy.value) return;
    go('finance');
    receiptCompany.value = companyId;
}
function chooseCompany(id: string) {
    if (!can('orders')) {
        error.value = '当前账号无施工单权限';
        return;
    }
    clearView();
    page.value = 'orders';
    company.value = id;
    pageNo.value = 1;
    void load().catch(() => {
    });
}
function reload() {
    if (page.value === 'finance-summary') {
        void financeSummaryPanel.value?.refresh();
        return;
    }
    if (page.value === 'expenses') {
        void expenseManagementPanel.value?.refresh();
        return;
    }
    if (page.value === 'ticket-issue') {
        void ticketIssuePanel.value?.refresh();
        return;
    }
    void load().catch(e => {
        if (!(e instanceof SessionChangedError))
            error.value = (e as Error).message;
    });
}
function navigateFinanceSource(target: FinanceDashboardSourceNavigation) {
    go(target.view, target);
    if (target.scope === 'snapshot')
        notice.value = '当前库存显示查询时的实时库存，不是汇总结束日期的历史库存快照。';
    else if (target.view === 'inventory' && target.filters?.kind === '进货')
        notice.value = `按财务汇总日期范围 ${target.filters.from} 至 ${target.filters.to} 查看有效进货单。`;
}
const rows = computed(() => catalogRows.value);
const label = (type: Entity, id: string) => references.value[type]?.find((x: CatalogRow) => x.id === id)?.name || '—';
function viewWarehouse(id: string) {
    if (!can('warehouse'))
        return;
    clearView();
    page.value = 'inventory';
    company.value = id;
    search.value = '';
    inventoryView.value = 'stock';
    reload();
}
async function transferWarehouse(id: string) {
    await openStock('调拨', id);
}
async function printOrder() {
    if (!can('customer_prices')) { error.value = '需要“查看客户价格”权限才能打印客户金额'; return; }
    if (!orderDraft.value.id) {
        error.value = '请先保存施工单后打印';
        return;
    }
    await perform(async () => {
        const order = await api<PrintOrder & {id:string}>('/order?id=' + encodeURIComponent(orderDraft.value.id));
        const [company, worker, settings] = await Promise.all([referenceById('companies', order.company_id), referenceById('workers', order.worker_id), api<{title?:string;company?:string}>('/print-settings')]);
        order.company_name = order.company_name || company?.name || '';
        order.worker_name = order.worker_name || worker?.name || '';
        order.print_settings = settings;
        printData.value = order;
    });
}
const permissionNames: Record<string, string> = { customer_prices: '查看客户价格', combo_pricing: '防水/丙纶组合免收', workers: '施工人管理', orders: '施工制单', orders_temporary: '临时工艺填写', order_delete: '删除施工单', customers: '客户与工地', rates: '工艺管理', warehouse: '仓储管理', wages: '工资管理', expenses: '报销与费用预支', finance: '应收款' };
async function deleteWarehouse(row: CatalogRow) {
    if (!confirm('删除未使用仓库“' + row.name + '”？已被单据使用的仓库不能删除。'))
        return;
    await perform(async () => {
        await submit('/warehouse-delete', { id: row.id, version: Number(row.version) });
        await load();
        notice.value = '仓库已删除';
    });
}
const filteredExport = computed(() => {
    const params = new URLSearchParams({ view: page.value, month: month.value, company_id: company.value, worker_id: workerFilter.value, q: search.value, status: page.value === 'wages' ? '' : status.value, wage_paid: page.value === 'wages' ? status.value : '' });
    if (page.value === 'orders' && financeSourceScope.value?.view === 'orders' && financeSourceScope.value.scope === 'period') {
        params.delete('month');
        params.set('from', sourceDateFrom.value);
        params.set('to', sourceDateTo.value);
    }
    return '/api/export?' + params;
});
watch(month, () => {
    pageNo.value = 1;
});
function onAccessRefresh() {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
    void refreshSessionAccess().catch(e => { if (!(e instanceof SessionChangedError)) error.value = '当前权限核验失败，请重新刷新：' + (e as Error).message; });
}
onMounted(() => {
    void init();
    if (typeof window !== 'undefined') window.addEventListener('focus', onAccessRefresh);
    if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onAccessRefresh);
    accessTimer = setInterval(onAccessRefresh, 30000);
});
onBeforeUnmount(() => {
    alive = false;
    stopSessionExpired();
    stopSessionValidate();
    if (typeof window !== 'undefined') window.removeEventListener('focus', onAccessRefresh);
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onAccessRefresh);
    if (accessTimer) clearInterval(accessTimer);
});
</script>

<template>
 <div v-if="!user" class="login"><form class="login-card" @submit.prevent="signIn"><div class="brand">特固施工</div><p>施工业务管理系统 <span class="app-version">{{ appVersion }}</span></p><label>账号<input v-model="login.username" autocomplete="username" required></label><label>密码<input v-model="login.password" type="password" autocomplete="current-password" required></label><p v-if="error" class="error">{{error}}</p><button class="primary" :disabled="busy">登录</button></form></div>
 <div v-else :key="user.id+':'+accessRevision" class="shell" :class="{'is-dashboard':page==='dashboard'}"><aside><div class="brand">特固施工<small>施工业务管理</small></div><nav><template v-for="m in menus" :key="m.name"><button v-if="m.id" :class="{active:page===m.id}" @click="go(m.id)">{{m.name}}</button><details v-else-if="m.children?.some(c=>menuAllowed(c.permission))" :open="m.children?.some(c=>c.id===page)"><summary>{{m.name}}</summary><button v-for="c in m.children?.filter(c=>menuAllowed(c.permission))" :key="c.id" :class="{active:page===c.id}" @click="go(c.id)">{{c.name}}</button></details></template></nav><div class="account"><button @click="go('account')">{{user.name}} · 我的账号</button><small>{{{admin:'系统管理员',operator:'普通管理员',employee:'员工'}[user.role as string]}}</small><button @click="signOut">退出登录</button></div></aside>
 <main v-if="pageAllowed(user,page)"><header><div class="workspace-title">工作空间 / {{title}}<span class="app-version">{{ appVersion }}</span></div><span>{{new Date().toLocaleDateString('zh-CN')}}</span></header><div class="heading"><div><h1>{{title}}</h1><p>特固施工 · 让业务有序进行</p></div><button @click="reload" :disabled="busy">刷新</button></div><div v-if="error" class="error" role="alert">{{error}}</div><div v-if="notice" class="notice" @click="notice=''">{{notice}} <small>点击关闭</small></div>
 <DashboardPanel v-if="page==='dashboard'" :revision="viewGeneration" :can="can" @navigate="go" /><AccountPanel v-if="page==='account'" :user="user" :permissions="permissionNames" @signed-out="clearSession('密码已修改，请重新登录')" />
 <ProjectsPanel ref="projectsPanel" v-if="page==='projects'" :can-maintain="can('orders')||can('customers')" :can-order="can('orders')" :can-finance="can('finance')" :can-manage-finance="user.role==='admin'||user.role==='operator'" :can-wages="can('wages')" :can-customer-prices="can('customer_prices')" :can-export="(can('orders')||can('finance'))&&can('customer_prices')" @open-order="openOrder" @open-receipts="openReceipts" />
 <WarehouseCatalog v-if="page==='products'||page==='warehouses'" :key="page" :kind="page" :items="catalogRows" :workers="references.workers" :busy="busy||loading" :error="error" :query="search" :status="catalogStatus" :page="pageNo" :total="total" @filter="filterCatalog" @page="catalogPage" @edit="editEntity" @toggle="toggleCatalog" @inventory="viewWarehouse" @transfer="transferWarehouse" @delete="deleteWarehouse"/>
 <template v-if="['companies','workers','crafts'].includes(page)">
 <PrintSettings v-if="page==='crafts'" /><div v-if="page==='crafts'" class="tabs"><button v-for="c in ['全部',...categories]" :class="{active:category===c}" @click="category=c;pageNo=1;reload()">{{c}}</button></div>
 <div v-if="page==='crafts'" class="toolbar"><label>显示范围<select v-model="catalogStatus" @change="pageNo=1;reload()"><option value="">全部</option><option value="1">启用</option><option value="0">停用</option></select></label></div>
 <section class="panel"><div class="toolbar"><input v-model="search" placeholder="搜索名称" autocomplete="off" @keyup.enter="pageNo=1;reload()"><button @click="pageNo=1;reload()">查询</button><span>共 {{total}} 项</span><button class="primary" :disabled="busy||loading" @click="editEntity()">＋ 新增{{page==='workers'?'施工人':title}}</button></div><div class="table-wrap"><table><thead><tr><th>名称</th><th>说明</th><th v-if="page==='workers'">施工工艺权限</th><th v-if="page==='crafts'">状态</th><th v-if="page==='crafts'">公共标准工资</th><th>操作</th></tr></thead><tbody><tr v-for="r in rows" :key="r.id"><td>{{r.name}}</td><td>{{page==='crafts'?r.category+' · '+r.mode:r.phone}}</td><td v-if="page==='workers'">{{(r.crafts||[]).join('、')||'未设置，不能制单'}}</td><td v-if="page==='crafts'">{{Number(r.active)?'启用':'停用'}}</td><td v-if="page==='crafts'">{{money(r.wage_cents)}}<small v-if="r.wage_test_only">模拟测试价</small></td><td><button class="link" :disabled="busy||loading" @click="editEntity(r)">{{page==='companies'?'编辑 / 固定工艺':'编辑'}}</button><button v-if="page==='companies' && can('orders')" class="link" @click="chooseCompany(r.id)">查看工地</button><button v-if="page==='companies' && can('finance') && can('customer_prices')" class="link" @click="openReceipts(r.id)">收款 / 欠款</button></td></tr><tr v-if="!rows.length"><td :colspan="page==='crafts'?5:page==='workers'?4:3" class="empty">{{loading?'正在读取资料…':error?'读取失败，请重试':'暂无符合条件的资料'}}</td></tr></tbody></table></div><div class="toolbar"><button :disabled="pageNo<=1" @click="catalogPage(pageNo-1)">上一页</button><span>第 {{pageNo}} / {{Math.max(1,Math.ceil(total/50))}} 页</span><button :disabled="pageNo*50>=total" @click="catalogPage(pageNo+1)">下一页</button></div></section></template>
 <ReceiptManagementPanel ref="ticketFinancePanel" v-if="page==='finance'&&can('finance')&&can('customer_prices')" :can-customer-prices="can('customer_prices')" :can-finance="can('finance')" :can-manage="user.role==='admin'||user.role==='operator'" :initial-company-id="receiptCompany" /><p v-else-if="page==='finance'" class="notice">此页面的客户金额需要“查看客户价格”权限；工资信息仍在工资管理中独立查看。</p>
 <TicketIssuePanel ref="ticketIssuePanel" v-if="page==='ticket-issue'&&can('finance')&&can('customer_prices')" :can-finance="can('finance')" :can-customer-prices="can('customer_prices')" @open-order="openOrder({id:$event})" />
 <WageFinancePanel ref="wageFinancePanel" v-if="page==='wages'" @archive="go('archives')" />
 <ExpenseManagementPanel ref="expenseManagementPanel" v-if="page==='expenses'&&can('expenses')" :allowed="can('expenses')" :initial-from="financeSourceScope?.view==='expenses'?financeSourceScope.filters?.from:undefined" :initial-to="financeSourceScope?.view==='expenses'?financeSourceScope.filters?.to:undefined" />
 <FinanceSummaryPanel ref="financeSummaryPanel" v-if="page==='finance-summary'&&can('finance')" :permissions="financeSummaryPermissions" @navigate="navigateFinanceSource" />
 <template v-if="page==='orders'"><div class="toolbar panel filter-toolbar"><template v-if="financeSourceScope?.view==='orders'&&financeSourceScope.scope==='period'"><label>开始日期<input type="date" v-model="sourceDateFrom" @change="pageNo=1;reload()"></label><label>结束日期<input type="date" v-model="sourceDateTo" @change="pageNo=1;reload()"></label></template><label v-else>月份<input type="month" v-model="month" @change="reload"></label><ReferenceSelect v-model="company" entity="companies" label="公司" empty-label="全部公司" search-placeholder="搜索公司" @change="pageNo=1;reload()" /><label>状态<select v-model="status" @change="pageNo=1;reload()"><option value="">全部</option><option v-for="s in ['进行中','待交单','已交单','部分收款','已收款','已取消']" :value="s">{{s}}</option></select></label><input v-model="search" aria-label="搜索工地或单号" placeholder="工地或单号" @keyup.enter="pageNo=1;reload()"><button @click="pageNo=1;reload()">查询</button><button v-if="page==='orders'" class="primary" @click="openOrder()">新增施工单</button><a v-if="can('customer_prices')" :href="filteredExport">导出当前筛选</a><span v-else class="hint">客户金额导出需要“查看客户价格”权限</span><button @click="go('archives')">月度归档</button></div><div class="notice">{{financeSourceScope?.view==='orders'?'财务汇总日期范围 '+sourceDateFrom+' 至 '+sourceDateTo+' · ':''}}{{total}} 张符合条件 · 有效金额合计 {{customerAmount(totalAmount)}} · 列表保留已取消记录及原金额 · 批量选择仅限当前页</div><section class="panel"><div class="table-wrap"><table><thead><tr><th>工地 / 单号</th><th>公司</th><th>日期</th><th>施工人</th><th>金额</th><th>状态</th><th></th></tr></thead><tbody><tr v-for="o in orderRows" :key="o.id"><td>{{o.address}}<small>{{o.number}}</small></td><td>{{o.company_name||label('companies',o.company_id)}}</td><td>{{o.work_date}}</td><td>{{(o.worker_names||o.worker_name)||label('workers',o.worker_id)}}</td><td>{{customerAmount(o.total_cents)}}</td><td><span class="tag">{{o.status}}</span></td><td><button class="link" @click="openOrder({id:sourceOrderId(o)})">查看 / 编辑</button></td></tr><tr v-if="!orderRows.length"><td colspan="7" class="empty">{{loading?'正在读取单据…':error?'读取失败，请重试':'所选日期范围内暂无符合条件单据'}}</td></tr></tbody></table></div><div class="toolbar"><button :disabled="pageNo<=1" @click="pageNo--;reload()">上一页</button><span>第 {{pageNo}} / {{Math.max(1,Math.ceil(total/100))}} 页</span><button :disabled="pageNo*100>=total" @click="pageNo++;reload()">下一页</button></div></section></template>

 <InventoryPanel v-if="page==='inventory'" :companies="references.companies" :warehouses="references.warehouses" :warehouse="company" :revision="historyRevision" :initial-tab="financeSourceScope?.view==='inventory'?financeSourceScope.filters?.inventoryTab:undefined" :initial-from="financeSourceScope?.view==='inventory'?financeSourceScope.filters?.from:undefined" :initial-to="financeSourceScope?.view==='inventory'?financeSourceScope.filters?.to:undefined" :initial-kind="financeSourceScope?.view==='inventory'?financeSourceScope.filters?.kind:undefined" @edit="editStock" @changed="reload" @create="openStock" />
 <section v-if="page==='users'" class="panel"><div class="toolbar"><input v-model="search" placeholder="搜索姓名或账号" @keyup.enter="pageNo=1;reload()"><button @click="pageNo=1;reload()">查询</button><span>共 {{total}} 个账号</span><button class="primary" @click="editUser()">＋ 新增账号</button></div><div class="table-wrap"><table><thead><tr><th>姓名 / 账号</th><th>角色</th><th>工艺权限</th><th>客户价格权限</th><th>状态</th><th></th></tr></thead><tbody><tr v-for="r in userRows"><td>{{r.name}}<small>{{r.username}}</small></td><td>{{r.role==='admin'?'系统管理员':r.role==='operator'?'普通管理员':'员工'}}</td><td>{{r.role!=='employee'?'全部工艺':r.crafts.length ? '已授权 '+r.crafts.length+' 项公共工艺' : '未开通'}}</td><td>{{r.role!=='employee'?'可查看客户价格':(r.permissions||[]).includes('customer_prices')?'可查看客户价格':'未授权客户价格'}}</td><td>{{Number(r.active)?'启用':'停用'}}</td><td><button @click="editUser(r)">编辑 / 重置密码</button><button v-if="r.role!=='admin'" @click="deleteUser(r)">删除</button></td></tr><tr v-if="!userRows.length"><td colspan="6" class="empty">{{loading?'正在读取账号…':error?'读取失败，请重试':'暂无符合条件的账号'}}</td></tr></tbody></table></div><div class="toolbar"><button :disabled="pageNo<=1" @click="catalogPage(pageNo-1)">上一页</button><span>第 {{pageNo}} / {{Math.max(1,Math.ceil(total/50))}} 页</span><button :disabled="pageNo*50>=total" @click="catalogPage(pageNo+1)">下一页</button></div></section>
 <section v-if="page==='audit'" class="panel"><HistoryPanel :revision="viewGeneration" /></section>
 <ArchivePanel v-if="page==='archives'" :companies="references.companies" :can="can" />
 <section v-if="page==='backups'" class="panel"><form class="backup-form" @submit.prevent="saveBackupConfig" autocomplete="off"><h3>备份与云存储</h3><div class="form-grid"><label>本地最近备份保留份数<input type="number" min="24" max="8760" v-model.number="backupConfig.retain" required></label><label>自动备份周期<select v-model.number="backupConfig.interval"><option :value="15">每15分钟</option><option :value="30">每30分钟</option><option :value="60">每小时</option><option :value="360">每6小时</option><option :value="1440">每天</option></select></label><label class="check"><input type="checkbox" v-model="backupConfig.enabled">同时上传S3兼容云存储</label><template v-if="backupConfig.enabled"><label>服务地址（HTTPS Endpoint）<input v-model="backupConfig.endpoint" placeholder="https://s3.example.com" required></label><label>区域<input v-model="backupConfig.region" required></label><label>存储桶<input v-model="backupConfig.bucket" required></label><label>路径前缀<input v-model="backupConfig.prefix"></label><label>Access Key<input v-model="backupConfig.access_key" required></label><label>Secret Key<input type="password" v-model="backupConfig.secret" :placeholder="backupConfig.has_secret?'已保存，留空不修改':'请输入密钥'" autocomplete="new-password"></label></template></div><p class="hint">云存储使用S3兼容接口。密钥加密保存；应用数据卷中的backup.key必须另行妥善备份。更换设备丢失密钥时可重新填写云凭据。云端历史文件由存储桶生命周期策略管理。</p><button class="primary" :disabled="busy">保存备份设置</button></form><div class="toolbar"><button class="primary" :disabled="busy" @click="backup">立即备份</button><button :disabled="busy||!backupConfig.enabled" @click="perform(async()=>{const r=await api<{message:string}>('/maintenance/cloud-check',{});notice=r.message})">检查已保存的云连接</button><span>每 {{backupConfig.interval||60}} 分钟自动备份</span></div><BackupConsole :revision="viewGeneration" /></section>
 </main></div>
 <OrderPrint v-if="user && printData && can('customer_prices') && (can('orders')||can('finance')||can('wages'))" :order="printData" :company="printData.company_name||label('companies',printData.company_id)" :worker="printData.worker_name||label('workers',printData.worker_id)" :can-wages="can('wages')" @close="printData=null" />
 <div v-if="user && dialog && pageAllowed(user,page)" class="overlay"><section class="modal" :class="{wide:dialog==='order'||dialog==='stock'||(dialog==='entity'&&catalogDraft.entity==='companies'),'order-dialog':dialog==='order'}"><div class="modal-head"><h2>{{dialog==='order'?'施工单':dialog==='stock'?stockDraft.kind:dialog==='user'?'账号与权限':dialog==='entity'&&catalogDraft.entity==='companies'?(catalogDraft.id?'编辑客户与工地':'新增客户与工地'):'编辑资料'}}</h2><button @click="dialog=''" :disabled="busy" aria-label="关闭">×</button></div><div v-if="error" class="error">{{error}}</div>
 <EntityEditor v-if="dialog==='entity'" :editing="catalogDraft" :categories="categories" :references="references" :busy="busy" :craft-category="craftCategory" :company-craft-options="companyCraftOptions" :company-fixed-visible="companyFixedVisible" :craft-page="companyCraftPage" :craft-total="companyCraftTotal" :can="can" :has-company-craft="hasCompanyCraft" @save="saveEntity" @select-category="selectCompanyCategory" @category="perform(()=>changeCompanyCraftCategory($event))" @search-craft="perform(()=>searchCompanyCrafts($event))" @craft-page="perform(()=>loadCompanyCraftOptions($event))" @toggle-craft="toggleCompanyCraft" @remove-craft="removeCompanyCraft" />

 <UserEditor v-if="dialog==='user'" :editing="userDraft" :categories="categories" :permission-names="permissionNames" :busy="busy" :can-assign-combinations="user.role==='admin'" @save="saveUser" />
 <OrderEditor v-if="dialog==='order'" :draft="orderDraft" :references="references" :projects="projectOptions" :package-options="companyPackages" :project-page="projectPage" :project-total="projectTotal" :worker-options="workerOptions" :company-craft-categories="companyCraftCategories" :selected-category="selectedOrderCategory" :inventory="inventory" :busy="busy" :order-locked="orderLocked" :order-warehouse="orderWarehouse" :allowed-crafts="allowedCrafts" :can="can" @save="saveOrder" @company="perform(onOrderCompanyChanged)" @project="projectChanged" @project-search="perform(()=>loadProjectOptions($event))" @project-page="perform(()=>loadProjectOptions(projectQuery,$event))" @category="perform(()=>orderCategoryChanged($event))" @worker="perform(()=>workerChanged($event))" @line-worker="lineWorkerChanged" @category-worker="categoryWorkerChanged" @add-location="addLocation" @remove-location="removeLocation" @material-product="materialProductSelected" @material-source="perform(refreshOrderInventory)" @print="printOrder" @cancel-order="cancelOrder" @temporary="addTemporary" @material="addMaterial" @sale="addSale" @close="dialog=''" />
 <StockEditor v-if="dialog==='stock'" :draft="stockDraft" :warehouses="references.warehouses" :busy="busy" @save="saveStock" @source="perform(stockSourceChanged)" @cancel="dialog=''" />
 </section></div>
</template>

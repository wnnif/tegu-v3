import { ref } from 'vue';
import { api } from '../api';
import { balances } from './references';
import {moneyCents,moneyFromCents,restoreStockInput,unitFactors} from '../stockQuantity';
import type { StockDraft, StockDocument, StockLine, CatalogRow, References, WorkflowContext } from './models';
export function useStock(ctx: WorkflowContext) {
    const { perform, references, inventory, user, dialog, submit, load, notice } = ctx;
    const draft = ref<StockDraft>({ kind: '进货', from: '', to: '', handler: '', date: '', note: '', lines: [] });
    function stockInput(l: Partial<StockLine> & {
        product_id: string;
        name: string;
        unit: string;
        qty_micro: import('./models').Numeric;
        price_cents: import('./models').Numeric;
        available: import('./models').Numeric;
    }): StockLine {
        const factors = unitFactors(l);
        const quantum = l.quantity_quantum != null ? String(l.quantity_quantum) : factors && /^\d+$/.test(String(l.qty_micro)) ? String(BigInt(String(l.qty_micro)) * factors.base) : '';
        const savedInput = l.input_quantity || l.input_qty;
        const restored = quantum && quantum !== '0' ? restoreStockInput(quantum, l.input_unit || 'base', savedInput || '', l) : null;
        const input_unit = restored?.unit || 'base';
        const input_qty = restored?.quantity || '';
        const input_amount = l.input_amount ?? (l.total_cents != null && quantum !== '0' ? moneyFromCents(l.total_cents) : '');
        return { ...l, input_unit, input_qty, input_quantity: input_qty, input_amount, quantity_quantum: quantum, note: l.note || '' };
    }
    async function editStock(row: StockDocument) {
        await perform(async () => {
            const refs = await api<References>('/reference?tables=products');
            references.value.products = refs.products;
            inventory.value = await balances(row.source_id);
            const lines: StockLine[] = (row.line_items || []).map((l: StockLine) => {
                const product = references.value.products.find((p: CatalogRow) => p.id === l.product_id);
                const balance = inventory.value.find(x => x.warehouse_id === row.source_id && x.product_id === l.product_id);
                return { ...product, ...l, name: l.name || product?.name || l.product_id, unit: l.unit || product?.unit || '', available: balance?.qty_micro || 0, available_quantum: balance?.quantity_quantum };
            });
            for (const p of references.value.products.filter((p: CatalogRow) => Number(p.active))) {
                if (!lines.some((l: StockLine) => l.product_id === p.id))
                    lines.push(stockInput({ ...p, name: p.name, unit: p.unit || '', product_id: p.id, qty_micro: 0, price_cents: 0, available: inventory.value.find(x => x.warehouse_id === row.source_id && x.product_id === p.id)?.qty_micro || 0, available_quantum: inventory.value.find(x => x.warehouse_id === row.source_id && x.product_id === p.id)?.quantity_quantum }));
            }
            draft.value = { ...row, handler: row.handler || user.value?.name || '', date: row.work_date, from: row.source_id, to: row.destination_id, version: Number(row.version), lines: lines.map(stockInput) };
            dialog.value = 'stock';
        });
    }
    async function openStock(kind: string, from = '') {
        await perform(async () => {
            const refs = await api<References>('/reference?tables=products');
            references.value.products = refs.products;
            draft.value = { kind, from: ['进货','报溢'].includes(kind) ? '' : from, to: ['进货','报溢'].includes(kind) ? from : '', handler: user.value?.name || '', date: new Date().toLocaleDateString('sv-SE'), note: '', lines: [] };
            await stockSourceChanged();
            dialog.value = 'stock';
        });
    }
    async function stockSourceChanged() {
        inventory.value = await balances(draft.value.from);
        if (draft.value.id) {
            for (const l of draft.value.lines)
                { const balance = inventory.value.find(x => x.warehouse_id === draft.value.from && x.product_id === l.product_id); l.available = balance?.qty_micro || 0; l.available_quantum = balance?.quantity_quantum; }
            return;
        }
        draft.value.lines = references.value.products.filter((p: CatalogRow) => Number(p.active)).map((p: CatalogRow) => ({ ...p, name: p.name, unit: p.unit || '', product_id: p.id, available: inventory.value.find(x => x.warehouse_id === draft.value.from && x.product_id === p.id)?.qty_micro || 0, available_quantum: inventory.value.find(x => x.warehouse_id === draft.value.from && x.product_id === p.id)?.quantity_quantum, qty_micro: 0, price_cents: 0 })).map(stockInput);
        if (!['进货', '报溢'].includes(draft.value.kind))
            draft.value.lines = draft.value.lines.filter(p => BigInt(String(p.available_quantum ?? p.available ?? 0)) > 0n);
    }
    async function saveStock() {
        await perform(async () => {
            if (['领料','退料','转料','调拨'].includes(draft.value.kind) && (!draft.value.from || !draft.value.to || draft.value.from === draft.value.to)) throw new Error('请选择不同的来源和去向位置');
            const lines = draft.value.lines.filter((l: StockLine) => l.quantity_quantum && BigInt(l.quantity_quantum) > 0n).map((l: StockLine) => ({
                product_id: l.product_id, input_unit: l.input_unit, input_quantity: l.input_qty,
                quantity_quantum: l.quantity_quantum, total_cents: draft.value.kind === '进货' ? Number(l.total_cents ?? NaN) : 0, note: l.note
            }));
            if (!lines.length) throw new Error('请填写至少一项商品数量');
            if (lines.some(l => BigInt(String(l.quantity_quantum)) > 1000000000000000n)) throw new Error('商品数量超出上限，请分开登记');
            if (draft.value.kind === '进货' && draft.value.lines.filter(l => l.quantity_quantum && BigInt(l.quantity_quantum) > 0n).some(l => !l.input_amount?.trim() || moneyCents(l.input_amount) === null)) throw new Error('请填写每种商品本次进货总金额，最多两位小数');
            if (draft.value.kind === '进货' && lines.some(l => !Number.isSafeInteger(l.total_cents) || l.total_cents < 0 || l.total_cents > 100000000000000)) throw new Error('请填写本次进货总金额，最多两位小数');
            await submit('/stock', { ...draft.value, lines });
            dialog.value = '';
            await load();
            notice.value = '库存操作已完成';
        });
    }
    return { stockDraft: draft, editStock, openStock, stockSourceChanged, saveStock, resetStock: () => {
            draft.value.lines = [];
        } };
}

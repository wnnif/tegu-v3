import type { Ref } from 'vue';
import type { SessionUser } from '../sessionAccess';
import type { OrderSettlement } from '../settlements';
export type Numeric = number | string;
export type Flag = boolean | number | string;
export type Entity = 'companies' | 'workers' | 'warehouses' | 'products' | 'crafts';
export interface CraftSubitem { id: string; name: string; wage_cents?: Numeric | null; }
// Shared selector projection. Fields are explicit; each API returns the fields of its entity.
export interface CatalogRow {
    id: string;
    name: string;
    version: number;
    active?: Flag;
    phone?: string;
    contact?: string;
    customer_type?: string;
    note?: string;
    crafts?: string[];
    worker_id?: string;
    worker_name?: string;
    brand?: string;
    spec?: string;
    color?: string;
    weight_kg?: Numeric;
    unit?: string;
    aux_unit?: string;
    aux_ratio?: Numeric;
    base_factor?: Numeric;
    aux_factor?: Numeric;
    category?: string;
    warehouse_type?: string;
    company_id?: string;
    mode?: string;
    price_cents?: Numeric;
    wage_cents?: Numeric;
    public_wage_cents?: Numeric;
    wage_configured?: boolean;
    wage_override_cents?: Numeric | null;
    customer_price_test_only?: boolean;
    wage_test_only?: boolean;
    ticket_mode?: 'single' | 'split';
    craft_category_order?: string[];
    source_id?: string;
    display_order?: number;
    subitems?: CraftSubitem[];
    ticket_slot?: number;
    included_by_craft_id?: string;
    source_missing?: boolean;
    source_active?: boolean;
    editable?: boolean;
}
export interface Craft extends CatalogRow {
    company_id: string;
    category: string;
    mode: string;
    price_cents?: Numeric;
    wage_cents?: Numeric;
    wage_configured?: boolean;
    source_id?: string;
}
export interface CatalogDraft extends CatalogRow {
    entity: Entity;
    crafts: string[];
    fixed_crafts: Craft[];
    active: boolean;
    worker_id: string;
    company_id: string;
    category: string;
    mode: string;
    price_cents: number;
    wage_cents: number;
    unit: string;
    aux_unit: string;
    aux_ratio: Numeric;
}
export interface References {
    companies: CatalogRow[];
    workers: CatalogRow[];
    warehouses: CatalogRow[];
    products: CatalogRow[];
    crafts: Craft[];
}
export interface LoginUser extends SessionUser {
    name: string;
    username: string;
    crafts: string[];
}
export interface UserRow {
    id: string;
    version: number;
    name: string;
    username: string;
    role: string;
    permissions: string[];
    crafts: string[];
    active: Flag;
}
export interface UserDraft extends UserRow {
    password: string;
    active: boolean;
}
export interface PageResult<T> {
    rows: T[];
    total: number;
    page: number;
    page_size?: number;
}
export interface OrderRow extends OrderSettlement {
    id: string;
    version: number;
    number: string;
    company_id: string;
    company_name?: string;
    worker_id: string;
    worker_name?: string;
    worker_names?: string;
    order_id?: string;
    address: string;
    work_date: string;
    status: string;
    wage_cents?: Numeric;
    wage_paid?: Flag;
    contact?: string;
    note?: string;
}
export interface OrderSegment {
    mode: string;
    length: Numeric;
    width: Numeric;
    height: Numeric;
    surface: string;
    amount_cents?: Numeric;
    count?: Numeric;
}
export interface OrderLine {
    id: string;
    client_key?: string; // Editor identity only; never sent to the server.
    client_ref?: string; // Request mapping shared by combinations and line consumables.
    craft_id: string;
    worker_id?: string;
    worker_name?: string;
    worker_override?: boolean;
    category: string;
    name: string;
    spec: string;
    region: string;
    subitem_id?: string;
    subitem_name?: string;
    actual_location?: string;
    sort_order?: number;
    editable?: boolean;
    ticket_claimed?: boolean;
    use_included_price?: boolean;
    package_included?: boolean;
    charge_mode?: 'normal' | 'free' | 'redo';
    charge_note?: string;
    included_by_craft_id?: string;
    description: string;
    mode: string;
    price_cents?: Numeric;
    wage_cents?: Numeric;
    wage_configured?: boolean;
    wage_override_cents?: Numeric | null;
    wage_adjustment_reason?: string;
    wage_adjustments?: readonly WageAdjustment[];
    temporary: boolean;
    entered: boolean;
    segments: OrderSegment[];
    qty_micro?: Numeric;
    waived_qty_micro?: Numeric;
    total_cents?: Numeric;
    labor_cents?: Numeric;
}
export interface WageAdjustment {
    id?: string;
    line_id?: string;
    line_deleted?: boolean;
    worker_name?: string;
    before_cents?: Numeric;
    after_cents?: Numeric;
    reason?: string;
    actor_name?: string;
    created_at?: string;
}
export interface Material {
    id?: string;
    product_id: string;
    warehouse_id: string;
    line_id?: string;
    qty_micro: Numeric;
    qty_quantum?: string;
    input_qty?: string;
    input_unit?: string;
    conversion_ratio?: string;
    product_name?: string;
    name?: string;
    unit?: string;
    aux_unit?: string;
    aux_ratio?: Numeric;
    warehouse_name?: string;
}
export interface OrderDraft extends OrderRow {
    saved_snapshot?: import('./readModels').SavedOrderRecord; // Editor-only original read snapshot; never submitted.
    project_id: string;
    package_id?: string;
    project_name?: string;
    project_number?: string;
    phone?: string;
    editable?: boolean;
    lines: OrderLine[];
    category_workers?: Record<string, string>;
    materials: Material[];
    sales?: import('../projectWork').Sale[];
    delivery_wage_cents?: Numeric;
    ticket_action?: 'normal' | 'correct' | 'append';
    ticket_id?: string;
    ticket_version?: number;
    sales_ticket_slot?: number;
    wage_records?: import('../printOrder').WageRecord[];
    wage_adjustments?: readonly WageAdjustment[];
    combinations?: import('../orderCombinations').ProjectCombination[];
    combination_version?: number;
    combination_dirty?: boolean; // Editor-only: omission preserves existing relations.
    combination_enabled?: boolean;
    combination_error?: string;
}
export interface Balance {
    warehouse_id: string;
    product_id: string;
    qty_micro: Numeric;
    quantity_quantum?: Numeric;
}
export interface StockLine {
    product_id: string;
    name: string;
    unit: string;
    spec?: string;
    color?: string;
    weight_kg?: Numeric;
    aux_unit?: string;
    aux_ratio?: Numeric;
    base_factor?: Numeric;
    aux_factor?: Numeric;
    qty_micro: Numeric;
    quantity_quantum?: string;
    price_cents: Numeric;
    total_cents?: Numeric;
    available: Numeric;
    available_quantum?: Numeric;
    input_unit: string;
    input_qty: string;
    input_quantity?: string;
    input_amount?: string;
    note: string;
}
export interface StockDocument {
    id: string;
    kind: string;
    work_date: string;
    source_id: string;
    destination_id: string;
    handler: string;
    version: Numeric;
    note: string;
    line_items: StockLine[];
}
export interface StockDraft {
    id?: string;
    version?: number;
    kind: string;
    from: string;
    to: string;
    handler: string;
    date: string;
    note: string;
    lines: StockLine[];
}
export interface BackupConfig {
    enabled: boolean;
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    access_key: string;
    secret: string;
    has_secret?: boolean;
    retain: number;
    interval: number;
}
export interface WorkflowContext {
    dialog: Ref<string>;
    error: Ref<string>;
    notice: Ref<string>;
    references: Ref<References>;
    user: Ref<LoginUser | null>;
    page: Ref<string>;
    company: Ref<string>;
    categories: Ref<string[]>;
    category: Ref<string>;
    inventory: Ref<Balance[]>;
    perform: (fn: () => Promise<void>) => Promise<void>;
    submit: (path: string, body: unknown) => Promise<unknown>;
    load: () => Promise<void>;
    can: (permission: string) => boolean;
}
export const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));

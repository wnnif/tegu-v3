import type { Numeric, Flag, Balance, WageAdjustment } from './models';
export interface InventoryRow extends Balance {
    order_id?: string;
    number?: string;
    work_date?: string;
    company?: string;
    address?: string;
    name: string;
    spec?: string;
    unit: string;
    warehouse_name?: string;
    warehouse?: string;
    warehouse_count?: number;
    quantity: Numeric;
    qty_quantum?: string;
    input_qty?: string;
    input_unit?: string;
    conversion_ratio?: string;
    base_factor?: Numeric;
    aux_factor?: Numeric;
    auxiliary_quantity?: Numeric;
    reference_value_cents?: Numeric;
    aux_unit?: string;
    aux_ratio?: Numeric;
    active?: Flag;
}
export interface MaintenanceJob {
    id: string;
    kind: string;
    status: string;
    stage: string;
    progress: number;
    total: number;
    message: string;
    created_at: string;
    result?: {
        count?: number;
        message?: string;
        file?: string;
        target?: string;
    };
}
export interface ArchiveFile {
    file: string;
    company_id?: string;
    company: string;
    kind: string;
    count: number;
    amount_cents?: Numeric;
    available: boolean;
}
export interface ArchiveRun {
    id: string;
    month: string;
    created_at: string;
    automatic: Flag;
    files: ArchiveFile[];
}
export interface ArchiveStatus {
    month: string;
    error?: string;
    time: string;
}
export interface ArchiveCompany {
    cash_received_cents?: Numeric;
    finance_scope?: string;
    key: string;
    run_id: string;
    company_id: string;
    company: string;
    month: string;
    created_at: string;
    automatic: Flag;
    legacy: boolean;
    files: ArchiveFile[];
    order_count?: number;
    construction_cents?: Numeric;
    receivable_cents?: Numeric;
    received_cents?: Numeric;
    outstanding_cents?: Numeric;
    wage_cents?: Numeric;
}
export interface ArchiveCompanyPage {
    rows: ArchiveCompany[];
    total: number;
    page: number;
    page_size: number;
    legacy_total: number;
    can_orders: boolean;
    can_finance: boolean;
    can_wages: boolean;
    latest: ArchiveStatus | null;
}
export interface BackupRun {
    id: string;
    created_at: string;
    status: string;
    file: string;
    bytes?: number;
    message: string;
    available: boolean;
}
export interface BackupReport {
    counts: Record<string, number>;
}

// Saved API snapshots only. Do not fill these fields from editor defaults or catalog references.
export interface SavedOrderSegment {
    readonly mode?: string;
    readonly length?: Numeric;
    readonly width?: Numeric;
    readonly height?: Numeric;
    readonly surface?: string;
    readonly count?: Numeric;
    readonly amount_cents?: Numeric;
}
export interface SavedOrderLine {
    readonly id: string;
    readonly craft_id: string;
    readonly category: string;
    readonly name: string;
    readonly mode: string;
    readonly spec?: string;
    readonly subitem_id?: string;
    readonly subitem_name?: string;
    readonly actual_location?: string;
    readonly region?: string;
    readonly sort_order?: number;
    readonly worker_id?: string;
    readonly worker_name?: string;
    readonly description?: string;
    readonly segments?: readonly SavedOrderSegment[];
    readonly qty_micro?: Numeric;
    readonly waived_qty_micro?: Numeric;
    readonly chargeable_qty_micro?: Numeric;
    readonly price_cents?: Numeric;
    readonly total_cents?: Numeric;
    readonly temporary?: boolean;
    readonly charge_note?: string;
    readonly use_included_price?: boolean;
    readonly charge_mode?: 'normal' | 'free' | 'redo';
    readonly wage_cents?: Numeric;
    readonly wage_override_cents?: Numeric | null;
    readonly wage_adjustment_reason?: string;
    readonly labor_cents?: Numeric;
    readonly wage_adjustments?: readonly WageAdjustment[];
}
export interface SavedOrderCombination {
    readonly id?: string;
    readonly version?: number;
    readonly waterproof_line_id: string;
    readonly polypropylene_line_id: string;
    readonly waterproof_order_id?: string;
    readonly polypropylene_order_id?: string;
    readonly waterproof_order_version?: number;
    readonly polypropylene_order_version?: number;
    readonly waive_direction: 'waterproof' | 'polypropylene' | '';
    readonly waived_qty_micro: Numeric;
}
export interface SavedCombinationLine extends SavedOrderLine {
    readonly order_id: string;
    readonly number: string;
    readonly work_date: string;
    readonly subitem_id: string;
    readonly subitem_name: string;
    readonly actual_location: string;
    readonly worker_id: string;
    readonly worker_name: string;
    readonly qty_micro: Numeric;
    readonly waived_qty_micro: Numeric;
    readonly chargeable_qty_micro: Numeric;
}
export interface SavedCombinationDetail {
    readonly id: string;
    readonly waterproof: SavedCombinationLine;
    readonly polypropylene: SavedCombinationLine;
}
export interface SavedOrderMaterial {
    readonly id?: string;
    readonly line_id?: string;
    readonly product_id: string;
    readonly warehouse_id: string;
    readonly qty_micro: Numeric;
    readonly qty_quantum?: string;
    readonly input_qty?: string;
    readonly input_unit?: string;
    readonly conversion_ratio?: string;
    readonly aux_unit?: string;
    readonly product_name?: string;
    readonly name?: string;
    readonly unit?: string;
    readonly warehouse_name?: string;
}
export interface SavedOrderSale extends SavedOrderMaterial {
    readonly id?: string;
    readonly sale_qty_micro?: Numeric;
    readonly price_unit?: string;
    readonly price_cents?: Numeric;
    readonly total_cents?: Numeric;
}
export interface SavedOrderRecord {
    readonly id: string;
    readonly company_id?: string;
    readonly company_name?: string;
    readonly address?: string;
    readonly number?: string;
    readonly work_date?: string;
    readonly worker_name?: string;
    readonly note?: string;
    readonly total_cents?: Numeric;
    readonly wage_cents?: Numeric;
    readonly delivery_wage_cents?: Numeric;
    readonly wage_records?: readonly import('../printOrder').WageRecord[];
    readonly wage_adjustments?: readonly WageAdjustment[];
    readonly lines: readonly SavedOrderLine[];
    readonly sales?: readonly SavedOrderSale[];
    readonly materials?: readonly SavedOrderMaterial[];
    readonly combinations?: readonly SavedOrderCombination[];
    readonly combination_details?: readonly SavedCombinationDetail[];
}
export interface SavedWorkDimension {
    readonly key: string;
    readonly mode: string;
    readonly text: string;
}
export interface SavedWorkMoney {
    readonly price_cents?: Numeric;
    readonly total_cents?: Numeric;
    readonly price_text: string;
    readonly amount_text: string;
}
export interface SavedWorkRow extends SavedWorkMoney {
    readonly key: string;
    readonly anchor: string;
    readonly id: string;
    readonly craft_id: string;
    readonly category: string;
    readonly name: string;
    readonly mode: string;
    readonly unit: string;
    readonly spec: string;
    readonly subitem_id: string;
    readonly subitem_name: string;
    readonly actual_location: string;
    readonly region: string;
    readonly worker_id: string;
    readonly worker_name: string;
    readonly description: string;
    readonly charge_note: string;
    readonly temporary: boolean;
    readonly dimensions: readonly SavedWorkDimension[];
    readonly qty_micro?: Numeric;
    readonly waived_qty_micro?: Numeric;
    readonly chargeable_qty_micro?: Numeric;
    readonly quantity_text: string;
    readonly waived_quantity_text: string;
    readonly chargeable_quantity_text: string;
    readonly has_waiver: boolean;
    readonly charge_mode?: 'normal' | 'free' | 'redo';
    readonly labor_cents?: Numeric;
    readonly wage_override_cents?: Numeric | null;
    readonly wage_adjustments?: readonly WageAdjustment[];
}
export interface SavedWorkCraft {
    readonly key: string;
    readonly craft_id: string;
    readonly name: string;
    readonly rows: readonly SavedWorkRow[];
}
export interface SavedWorkCategory {
    readonly key: string;
    readonly category: string;
    readonly crafts: readonly SavedWorkCraft[];
}
export interface SavedWorkMaterial {
    readonly key: string;
    readonly id: string;
    readonly line_id: string;
    readonly product_id: string;
    readonly product_name: string;
    readonly warehouse_name: string;
    readonly quantity_text: string;
}
export interface SavedWorkSale extends SavedWorkMaterial, SavedWorkMoney {
    readonly price_unit: string;
    readonly sale_quantity_text: string;
}
export interface SavedCombinationLineView extends SavedWorkMoney {
    readonly kind: 'waterproof' | 'polypropylene';
    readonly id: string;
    readonly order_id: string;
    readonly number: string;
    readonly work_date: string;
    readonly name: string;
    readonly category: string;
    readonly subitem_name: string;
    readonly actual_location: string;
    readonly worker_name: string;
    readonly quantity_text: string;
    readonly waived_quantity_text: string;
    readonly chargeable_quantity_text: string;
    readonly has_details: boolean;
    readonly anchor?: string;
}
export interface SavedWorkCombination {
    readonly key: string;
    readonly id: string;
    readonly direction: SavedOrderCombination['waive_direction'];
    readonly direction_text: string;
    readonly waived_quantity_text: string;
    readonly cross_date: boolean;
    readonly waterproof: SavedCombinationLineView;
    readonly polypropylene: SavedCombinationLineView;
}
export interface SavedOrderWorkView {
    readonly id: string;
    readonly categories: readonly SavedWorkCategory[];
    readonly sales: readonly SavedWorkSale[];
    readonly materials: readonly SavedWorkMaterial[];
    readonly combinations: readonly SavedWorkCombination[];
    readonly combination_enabled: boolean;
    readonly material_worker_name: string;
    readonly note: string;
    readonly total_cents?: Numeric;
    readonly wage_records?: readonly import('../printOrder').WageRecord[];
    readonly wage_adjustments?: readonly WageAdjustment[];
}

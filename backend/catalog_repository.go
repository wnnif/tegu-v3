package main

import (
	"database/sql"
	"encoding/json"
)

// Explicit projections prevent a new database column from silently becoming an API field.
// Decimal inventory metadata stays a decimal string, matching the existing wire contract.
type catalogPage struct {
	Rows     any   `json:"rows"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}
type companiesRecord struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Contact            *string  `json:"contact,omitempty"`
	Phone              *string  `json:"phone,omitempty"`
	Version            int64    `json:"version"`
	CustomerType       string   `json:"customer_type"`
	Note               *string  `json:"note,omitempty"`
	TicketMode         string   `json:"ticket_mode"`
	CraftCategoryOrder []string `json:"craft_category_order"`
}
type workersRecord struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Phone   *string  `json:"phone,omitempty"`
	Version int64    `json:"version"`
	UserID  *string  `json:"user_id"`
	Crafts  []string `json:"crafts"`
}
type warehousesRecord struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	WorkerID      *string `json:"worker_id"`
	Active        int64   `json:"active"`
	Version       int64   `json:"version"`
	Category      string  `json:"category"`
	WarehouseType string  `json:"warehouse_type"`
	Note          string  `json:"note"`
	WorkerName    string  `json:"worker_name"`
}
type productsRecord struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Unit     string `json:"unit"`
	AuxUnit  string `json:"aux_unit"`
	AuxRatio string `json:"aux_ratio"`
	Active   int64  `json:"active"`
	Version  int64  `json:"version"`
	Color    string `json:"color"`
	WeightKG string `json:"weight_kg"`
	Brand    string `json:"brand"`
	Spec     string `json:"spec"`
	Note     string `json:"note"`
}
type craftsRecord struct {
	customerPricesHidden bool
	ID                   string         `json:"id"`
	CompanyID            string         `json:"company_id"`
	Category             string         `json:"category"`
	Name                 string         `json:"name"`
	Mode                 string         `json:"mode"`
	PriceCents           int64          `json:"price_cents"`
	WageCents            *int64         `json:"wage_cents,omitempty"`
	PublicWageCents      *int64         `json:"public_wage_cents,omitempty"`
	WageConfigured       bool           `json:"wage_configured"`
	WageOverrideCents    *int64         `json:"wage_override_cents,omitempty"`
	CustomerPriceTestOnly bool          `json:"customer_price_test_only,omitempty"`
	WageTestOnly          bool          `json:"wage_test_only,omitempty"`
	Active               int64          `json:"active"`
	Version              int64          `json:"version"`
	Spec                 string         `json:"spec"`
	SourceID             string         `json:"source_id"`
	Subitems             []CraftSubitem `json:"subitems"`
	TicketSlot           int            `json:"ticket_slot"`
	IncludedByCraftID    string         `json:"included_by_craft_id"`
	DisplayOrder         int            `json:"display_order"`
	SourceMissing        bool           `json:"source_missing"`
	SourceActive         bool           `json:"source_active"`
	Editable             bool           `json:"editable"`
}

// source/suffix are built only by the validated catalog handlers; values use placeholders.
func readCatalog(q rowQuery, table, source, suffix string, args []any, u User) (any, error) {
	switch table {
	case "companies":
		return scanRows[companiesRecord](q, "SELECT id,name,contact,phone,version,customer_type,note,ticket_mode,craft_category_order FROM "+source+suffix, args, func(r *sql.Rows, item *companiesRecord) error {
			var raw []byte
			if err := r.Scan(&item.ID, &item.Name, &item.Contact, &item.Phone, &item.Version, &item.CustomerType, &item.Note, &item.TicketMode, &raw); err != nil {
				return err
			}
			var err error
			item.CraftCategoryOrder, err = readCompanyCraftCategoryOrder(raw)
			if err != nil {
				return err
			}
			if !u.can("customers") {
				item.Contact = nil
				item.Phone = nil
				item.Note = nil
			}
			return nil
		})
	case "workers":
		return scanRows[workersRecord](q, "SELECT id,name,phone,version,user_id,crafts FROM "+source+suffix, args, func(r *sql.Rows, item *workersRecord) error {
			var raw []byte
			if err := r.Scan(&item.ID, &item.Name, &item.Phone, &item.Version, &item.UserID, &raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &item.Crafts); err != nil {
				return err
			}
			if item.Crafts == nil {
				item.Crafts = []string{}
			}
			if !u.can("workers") && !u.can("wages") {
				item.Phone = nil
			}
			return nil
		})
	case "warehouses":
		return scanRows[warehousesRecord](q, "SELECT id,name,worker_id,active,version,category,warehouse_type,note,worker_name FROM "+source+suffix, args, func(r *sql.Rows, item *warehousesRecord) error {
			if err := r.Scan(&item.ID, &item.Name, &item.WorkerID, &item.Active, &item.Version, &item.Category, &item.WarehouseType, &item.Note, &item.WorkerName); err != nil {
				return err
			}
			return nil
		})
	case "products":
		return scanRows[productsRecord](q, "SELECT id,name,unit,aux_unit,aux_ratio,active,version,color,weight_kg,brand,spec,note FROM "+source+suffix, args, func(r *sql.Rows, item *productsRecord) error {
			if err := r.Scan(&item.ID, &item.Name, &item.Unit, &item.AuxUnit, &item.AuxRatio, &item.Active, &item.Version, &item.Color, &item.WeightKG, &item.Brand, &item.Spec, &item.Note); err != nil {
				return err
			}
			return nil
		})
	case "crafts":
		return scanRows[craftsRecord](q, "SELECT id,company_id,category,name,mode,price_cents,wage_cents,public_wage_cents,wage_configured,wage_override_cents,active,version,spec,source_id,subitems,ticket_slot,included_by_craft_id,display_order,customer_price_test_only,wage_test_only,source_missing,source_active FROM "+craftConfigurationSource+suffix, args, func(r *sql.Rows, item *craftsRecord) error {
			var raw []byte
			var wageOverride sql.NullInt64
			if err := r.Scan(&item.ID, &item.CompanyID, &item.Category, &item.Name, &item.Mode, &item.PriceCents, &item.WageCents, &item.PublicWageCents, &item.WageConfigured, &wageOverride, &item.Active, &item.Version, &item.Spec, &item.SourceID, &raw, &item.TicketSlot, &item.IncludedByCraftID, &item.DisplayOrder, &item.CustomerPriceTestOnly, &item.WageTestOnly, &item.SourceMissing, &item.SourceActive); err != nil {
				return err
			}
			if (u.can("rates") || u.can("wages")) && item.CompanyID != "" && wageOverride.Valid {
				value := wageOverride.Int64
				item.WageOverrideCents = &value
			}
			item.Subitems = []CraftSubitem{}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &item.Subitems); err != nil {
					return err
				}
			}
			if !u.can("rates") && !u.can("wages") {
				for i := range item.Subitems {
					item.Subitems[i].WageCents = nil
				}
			}
			item.customerPricesHidden = !u.can("customer_prices")
			item.Editable = item.CompanyID != "" && item.Active != 0 && item.SourceActive && !item.SourceMissing && u.canFillCraft(item.SourceID)
			if !u.can("rates") && !u.can("wages") {
				item.WageCents = nil
				item.PublicWageCents = nil
				item.WageOverrideCents = nil
				item.WageTestOnly = false
			}
			if !u.can("customer_prices") {
				item.CustomerPriceTestOnly = false
			}
			return nil
		})
	}
	return nil, fail(400, "基础资料类别无效")
}

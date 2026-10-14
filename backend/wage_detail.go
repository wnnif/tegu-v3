package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

// Wage details are deliberately separate from construction editing DTOs. Even
// a wage-authorized reader receives only the selected account's saved work and
// wages here, never another worker's rows, customer charges or material prices.
type wageDetailSegment struct {
	Length  float64 `json:"length"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Surface string  `json:"surface"`
	Count   int64   `json:"count,omitempty"`
	Mode    string  `json:"mode,omitempty"`
}

type wageDetailLine struct {
	ID          string              `json:"id"`
	Category    string              `json:"category"`
	Name        string              `json:"name"`
	Spec        string              `json:"spec"`
	Region      string              `json:"region"`
	Description string              `json:"description"`
	Mode        string              `json:"mode"`
	SubitemID   string              `json:"subitem_id"`
	SortOrder   int                 `json:"sort_order"`
	WorkerID    string              `json:"worker_id"`
	WorkerName  string              `json:"worker_name"`
	Qty         int64               `json:"qty_micro"`
	Wage        *int64              `json:"wage_cents,omitempty"`
	Labor       *int64              `json:"labor_cents,omitempty"`
	WageConfigured bool              `json:"wage_configured"`
	WageOverrideCents *int64         `json:"wage_override_cents,omitempty"`
	Segments    []wageDetailSegment `json:"segments"`
}

type wageDetailView struct {
	ID              string           `json:"id"`
	OrderID         string           `json:"order_id"`
	Number          string           `json:"number"`
	CompanyID       string           `json:"company_id"`
	CompanyName     string           `json:"company_name"`
	ProjectID       string           `json:"project_id"`
	ProjectName     string           `json:"project_name"`
	Address         string           `json:"address"`
	WorkDate        string           `json:"work_date"`
	WorkerID        string           `json:"worker_id"`
	WorkerName      string           `json:"worker_name"`
	Status          string           `json:"status"`
	WageCents       *int64           `json:"wage_cents,omitempty"`
	WageConfigured  bool             `json:"wage_configured"`
	WagePaid        bool             `json:"wage_paid"`
	Version         int64            `json:"version"`
	DeliveryWage    int64            `json:"delivery_wage_cents"`
	Legacy          bool             `json:"legacy"`
	HandlesDelivery bool             `json:"handles_delivery"`
	Lines           []wageDetailLine `json:"lines"`
}

func (a *App) readWage(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资查看权限")
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("id"))
	if accountID == "" || len(accountID) > 64 {
		return fail(400, "请选择有效的工资记录")
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	detail, err := readWageDetail(tx, accountID)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, detail)
	return nil
}

func readWageDetail(tx *sql.Tx, accountID string) (wageDetailView, error) {
	d := wageDetailView{Lines: []wageDetailLine{}}
	var wageCents int64
	err := tx.QueryRow(`SELECT ww.id,ww.order_id,o.number,o.company_id,COALESCE(NULLIF(o.company_name,''),c.name,''),o.project_id,COALESCE(NULLIF(o.project_name,''),p.name,''),o.address,DATE_FORMAT(o.work_date,'%Y-%m-%d'),ww.worker_id,ww.worker_name,o.status,ww.wage_cents,ww.wage_configured,ww.wage_paid,ww.version,ww.delivery_wage_cents,COALESCE(saved.legacy,1),
		(ww.worker_id=o.worker_id AND (o.delivery_wage_cents>0 OR EXISTS(SELECT 1 FROM order_sales s WHERE s.order_id=o.id)))
		FROM `+workerWageSource+` ww JOIN orders o ON o.id=ww.order_id LEFT JOIN companies c ON c.id=o.company_id LEFT JOIN projects p ON p.id=o.project_id LEFT JOIN order_worker_wages saved ON saved.id=ww.id WHERE ww.id=?`, accountID).Scan(&d.ID, &d.OrderID, &d.Number, &d.CompanyID, &d.CompanyName, &d.ProjectID, &d.ProjectName, &d.Address, &d.WorkDate, &d.WorkerID, &d.WorkerName, &d.Status, &wageCents, &d.WageConfigured, &d.WagePaid, &d.Version, &d.DeliveryWage, &d.Legacy, &d.HandlesDelivery)
	if err == sql.ErrNoRows {
		return d, fail(404, "工资记录不存在")
	}
	if err != nil {
		return d, err
	}
	if d.WageConfigured {
		d.WageCents = &wageCents
	}
	if err = validateOrderWorkerAccount(tx, d.OrderID); err != nil {
		return d, err
	}
	lines, err := scanRows[wageDetailLine](tx, `SELECT l.id,l.category,l.name,l.spec,l.region,l.description,l.mode,l.subitem_id,l.sort_order,COALESCE(NULLIF(l.worker_id,''),o.worker_id),COALESCE(NULLIF(l.worker_name,''),?),l.qty_micro,l.wage_cents,l.labor_cents,l.wage_configured,l.wage_override_cents,l.segments FROM order_lines l JOIN orders o ON o.id=l.order_id WHERE l.order_id=? AND COALESCE(NULLIF(l.worker_id,''),o.worker_id)=? ORDER BY l.sort_order,l.id`, []any{d.WorkerName, d.OrderID, d.WorkerID}, func(r *sql.Rows, v *wageDetailLine) error {
		var raw string
		var override sql.NullInt64
		var wage, labor int64
		if err := r.Scan(&v.ID, &v.Category, &v.Name, &v.Spec, &v.Region, &v.Description, &v.Mode, &v.SubitemID, &v.SortOrder, &v.WorkerID, &v.WorkerName, &v.Qty, &wage, &labor, &v.WageConfigured, &override, &raw); err != nil {
			return err
		}
		if override.Valid {
			value := override.Int64
			v.WageOverrideCents = &value
		}
		if v.WageConfigured || v.WageOverrideCents != nil {
			v.Wage = &wage
			v.Labor = &labor
		}
		v.Segments = []wageDetailSegment{}
		if err := json.Unmarshal([]byte(raw), &v.Segments); err != nil {
			return err
		}
		if v.Segments == nil {
			v.Segments = []wageDetailSegment{}
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	d.Lines = lines
	return d, nil
}

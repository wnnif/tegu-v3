package main

import "database/sql"

type projectRecord struct {
	customerPricesHidden      bool
	financeHidden             bool
	ID                        string `json:"id"`
	Number                    string `json:"number"`
	CompanyID                 string `json:"company_id"`
	Name                      string `json:"name"`
	Address                   string `json:"address"`
	Status                    string `json:"status"`
	Version                   int64  `json:"version"`
	CreatedAt                 string `json:"created_at"`
	CompanyName               string `json:"company_name"`
	OrderCount                int64  `json:"order_count"`
	TotalCents                int64  `json:"total_cents"`
	ReceivedCents             int64  `json:"received_cents"`
	UndeliveredCents          int64  `json:"undelivered_cents"`
	DeliveredOutstandingCents int64  `json:"delivered_outstanding_cents"`
	OutstandingCents          int64  `json:"outstanding_cents"`
	AllocatedCents            int64  `json:"allocated_cents"`
	DiscountCents             int64  `json:"discount_cents"`
	DeliveredCents            int64  `json:"delivered_cents"`
	UnsettledCents            int64  `json:"unsettled_cents"`
}

type projectOrderRecord struct {
	customerPricesHidden bool
	financeHidden        bool
	WorkerNames          string `json:"worker_names"`
	ID                   string `json:"id"`
	Number               string `json:"number"`
	WorkDate             string `json:"work_date"`
	Address              string `json:"address"`
	Status               string `json:"status"`
	TotalCents           int64  `json:"total_cents"`
	ReceivedCents        int64  `json:"received_cents"`
	OutstandingCents     int64  `json:"outstanding_cents"`
	CompanyID            string `json:"company_id"`
	CompanyName          string `json:"company_name"`
	WorkerID             string `json:"worker_id"`
	WorkerName           string `json:"worker_name"`
}

type unassignedProjectOrderRecord struct {
	customerPricesHidden bool
	ID                   string `json:"id"`
	Number               string `json:"number"`
	WorkDate             string `json:"work_date"`
	Address              string `json:"address"`
	Status               string `json:"status"`
	TotalCents           int64  `json:"total_cents"`
	Version              int64  `json:"version"`
}

type projectDetailPage struct {
	Project projectRecord        `json:"project"`
	Orders  []projectOrderRecord `json:"orders"`
	Page    int                  `json:"page"`
}

func scanProjectRecords(tx *sql.Tx, query string, args []any, u User) ([]projectRecord, error) {
	return scanRows[projectRecord](tx, query, args, func(r *sql.Rows, item *projectRecord) error {
		item.customerPricesHidden, item.financeHidden = !u.can("customer_prices"), !u.can("finance")
		return r.Scan(&item.ID, &item.Number, &item.CompanyID, &item.Name, &item.Address, &item.Status,
			&item.Version, &item.CreatedAt, &item.CompanyName, &item.OrderCount, &item.TotalCents,
			&item.ReceivedCents, &item.UndeliveredCents, &item.DeliveredOutstandingCents, &item.OutstandingCents)
	})
}

func scanProjectOrders(tx *sql.Tx, query string, args []any, u User) ([]projectOrderRecord, error) {
	return scanRows[projectOrderRecord](tx, query, args, func(r *sql.Rows, item *projectOrderRecord) error {
		item.customerPricesHidden, item.financeHidden = !u.can("customer_prices"), !u.can("finance")
		var raw []byte
		if err := r.Scan(&item.ID, &item.Number, &item.WorkDate, &item.Address, &item.Status,
			&item.TotalCents, &item.ReceivedCents, &item.OutstandingCents, &item.CompanyID,
			&item.CompanyName, &item.WorkerID, &item.WorkerName, &raw); err != nil {
			return err
		}
		var err error
		item.WorkerNames, err = decodeWorkerNames(raw)
		return err
	})
}

func scanUnassignedProjectOrders(tx *sql.Tx, query string, args []any, u User) ([]unassignedProjectOrderRecord, error) {
	return scanRows[unassignedProjectOrderRecord](tx, query, args, func(r *sql.Rows, item *unassignedProjectOrderRecord) error {
		item.customerPricesHidden = !u.can("customer_prices")
		return r.Scan(&item.ID, &item.Number, &item.WorkDate, &item.Address, &item.Status, &item.TotalCents, &item.Version)
	})
}

package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
)

type packageCoverageSnapshot struct {
	ItemName       string `json:"item_name"`
	CraftID        string `json:"craft_id"`
	SourceCraftID  string `json:"source_craft_id"`
	SubitemID      string `json:"subitem_id"`
	Mapping        string `json:"mapping"`
}

type projectPackageSnapshot struct {
	ProjectID      string                    `json:"project_id"`
	SeedID         string                    `json:"seed_id"`
	PackageID      string                    `json:"package_id"`
	CompanyID      string                    `json:"company_id"`
	Name           string                    `json:"name"`
	RoomCount      int                       `json:"room_count"`
	PriceCents     int64                     `json:"price_cents"`
	SourceID       string                    `json:"source_id"`
	SourceSheet    string                    `json:"source_sheet"`
	SourceRow      int                       `json:"source_row"`
	Coverage       []packageCoverageSnapshot `json:"coverage"`
	ChargedTicket  string                    `json:"charged_ticket_id,omitempty"`
	CreatedBy      string                    `json:"created_by"`
}

type projectPackageView struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	RoomCount int                       `json:"room_count"`
	Price     *int64                    `json:"price_cents,omitempty"`
	Coverage  []packageCoverageSnapshot `json:"coverage"`
}

type projectPackageChargeView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RoomCount int    `json:"room_count"`
	Amount    int64  `json:"amount_cents"`
}

type companyPackageOption struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	RoomCount int                       `json:"room_count"`
	Price     int64                     `json:"price_cents"`
	Coverage  []packageCoverageSnapshot `json:"coverage"`
}

const projectPackageMigrationVersion = 454

func migrateProjectPackages(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS project_packages(
		project_id VARCHAR(64) PRIMARY KEY,
		seed_id VARCHAR(100) NOT NULL,
		package_id VARCHAR(64) NOT NULL,
		company_id VARCHAR(64) NOT NULL,
		name VARCHAR(200) NOT NULL,
		room_count INT NOT NULL,
		price_cents BIGINT NOT NULL,
		source_id VARCHAR(64) NOT NULL,
		source_sheet VARCHAR(100) NOT NULL,
		source_row INT NOT NULL,
		coverage JSON NOT NULL,
		charged_ticket_id VARCHAR(64) NULL,
		created_by VARCHAR(64) NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uq_project_package_ticket(charged_ticket_id),
		INDEX idx_project_package_company(company_id,package_id),
		CONSTRAINT fk_project_package_project FOREIGN KEY(project_id) REFERENCES projects(id),
		CONSTRAINT fk_project_package_company FOREIGN KEY(company_id) REFERENCES companies(id),
		CONSTRAINT fk_project_package_catalog FOREIGN KEY(seed_id,package_id) REFERENCES catalog_seed_packages(seed_id,package_id),
		CONSTRAINT fk_project_package_ticket FOREIGN KEY(charged_ticket_id) REFERENCES project_tickets(id),
		CHECK(room_count BETWEEN 2 AND 5 AND price_cents BETWEEN 0 AND 100000000000000)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_lines' AND COLUMN_NAME='package_included'`).Scan(&columnCount); err != nil {
		return err
	}
	if columnCount == 0 {
		if _, err := db.Exec(`ALTER TABLE order_lines ADD COLUMN package_included BOOLEAN NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", projectPackageMigrationVersion)
	return err
}

func readCatalogPackageCoverage(tx *sql.Tx, seedID, packageID, companyID string) ([]packageCoverageSnapshot, error) {
	rows, err := tx.Query(`SELECT item_name,craft_id,subitem_id,mapping FROM catalog_seed_package_coverage WHERE seed_id=? AND package_id=? ORDER BY item_name`, seedID, packageID)
	if err != nil {
		return nil, err
	}
	coverage := []packageCoverageSnapshot{}
	for rows.Next() {
		var item packageCoverageSnapshot
		if err = rows.Scan(&item.ItemName, &item.SourceCraftID, &item.SubitemID, &item.Mapping); err != nil {
			rows.Close()
			return nil, err
		}
		coverage = append(coverage, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range coverage {
		item := &coverage[i]
		if item.Mapping == "mapped" {
			var foundID string
			mapErr := tx.QueryRow(`SELECT id FROM crafts WHERE company_id=? AND source_id=? ORDER BY id LIMIT 2`, companyID, item.SourceCraftID).Scan(&foundID)
			if mapErr == nil {
				var duplicate string
				if dupErr := tx.QueryRow(`SELECT id FROM crafts WHERE company_id=? AND source_id=? AND id<>? LIMIT 1`, companyID, item.SourceCraftID, foundID).Scan(&duplicate); dupErr == nil {
					return nil, fail(409, "套餐覆盖工艺在该公司存在多个固定工艺对应，无法安全应用")
				} else if dupErr != sql.ErrNoRows {
					return nil, dupErr
				}
				item.CraftID = foundID
			} else if mapErr != sql.ErrNoRows {
				return nil, mapErr
			}
		}
	}
	return coverage, nil
}

func loadCatalogPackage(tx *sql.Tx, companyID, packageID string) (projectPackageSnapshot, error) {
	var pkg projectPackageSnapshot
	err := tx.QueryRow(`SELECT p.seed_id,p.package_id,p.company_id,p.name,p.room_count,p.price_cents,p.source_id,p.source_sheet,p.source_row
		FROM catalog_seed_packages p JOIN catalog_seed_imports i ON i.seed_id=p.seed_id
		WHERE p.company_id=? AND p.package_id=? AND p.classification='package'`, companyID, packageID).
		Scan(&pkg.SeedID, &pkg.PackageID, &pkg.CompanyID, &pkg.Name, &pkg.RoomCount, &pkg.PriceCents, &pkg.SourceID, &pkg.SourceSheet, &pkg.SourceRow)
	if err == sql.ErrNoRows {
		return pkg, fail(400, "套餐不存在或不属于所选公司，请刷新后重新选择")
	}
	if err != nil {
		return pkg, err
	}
	pkg.Coverage, err = readCatalogPackageCoverage(tx, pkg.SeedID, pkg.PackageID, pkg.CompanyID)
	if err != nil {
		return pkg, err
	}
	return pkg, nil
}

func insertProjectPackage(tx *sql.Tx, projectID, companyID, packageID, actorID string) (projectPackageSnapshot, error) {
	pkg, err := loadCatalogPackage(tx, companyID, packageID)
	if err != nil {
		return pkg, err
	}
	pkg.ProjectID, pkg.CreatedBy = projectID, actorID
	coverageJSON, err := json.Marshal(pkg.Coverage)
	if err != nil {
		return pkg, err
	}
	if _, err = tx.Exec(`INSERT INTO project_packages(project_id,seed_id,package_id,company_id,name,room_count,price_cents,source_id,source_sheet,source_row,coverage,created_by)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, pkg.ProjectID, pkg.SeedID, pkg.PackageID, pkg.CompanyID, pkg.Name, pkg.RoomCount, pkg.PriceCents, pkg.SourceID, pkg.SourceSheet, pkg.SourceRow, string(coverageJSON), pkg.CreatedBy); err != nil {
		return pkg, err
	}
	return pkg, nil
}

func readProjectPackage(tx *sql.Tx, projectID string, lock bool) (*projectPackageSnapshot, error) {
	query := `SELECT project_id,seed_id,package_id,company_id,name,room_count,price_cents,source_id,source_sheet,source_row,coverage,COALESCE(charged_ticket_id,''),created_by FROM project_packages WHERE project_id=?`
	if lock {
		query += " FOR UPDATE"
	}
	var pkg projectPackageSnapshot
	var coverage []byte
	err := tx.QueryRow(query, projectID).Scan(&pkg.ProjectID, &pkg.SeedID, &pkg.PackageID, &pkg.CompanyID, &pkg.Name, &pkg.RoomCount, &pkg.PriceCents, &pkg.SourceID, &pkg.SourceSheet, &pkg.SourceRow, &coverage, &pkg.ChargedTicket, &pkg.CreatedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(coverage, &pkg.Coverage); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func packageCoversLine(pkg *projectPackageSnapshot, craftID, subitemID string) bool {
	if pkg == nil || craftID == "" {
		return false
	}
	for _, item := range pkg.Coverage {
		if item.Mapping == "mapped" && item.CraftID != "" && item.CraftID == craftID && item.SubitemID == subitemID {
			return true
		}
	}
	return false
}

func attachProjectPackageCharge(tx *sql.Tx, u User, projectID, ticketID string) error {
	pkg, err := readProjectPackage(tx, projectID, true)
	if err != nil || pkg == nil || pkg.ChargedTicket != "" {
		return err
	}
	var owner string
	if err = tx.QueryRow("SELECT project_id FROM project_tickets WHERE id=? FOR UPDATE", ticketID).Scan(&owner); err != nil {
		return err
	}
	if owner != projectID {
		return fail(409, "套餐收费目标票据不属于该工地")
	}
	result, err := tx.Exec(`UPDATE project_packages SET charged_ticket_id=? WHERE project_id=? AND charged_ticket_id IS NULL`, ticketID, projectID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fail(409, "套餐收费票据已变化，请刷新工地后重试")
	}
	return audit(tx, u, "挂接工地套餐收费", projectID, map[string]any{
		"package_id": pkg.PackageID, "name": pkg.Name, "room_count": pkg.RoomCount,
		"amount_cents": pkg.PriceCents, "ticket_id": ticketID,
	})
}

func projectPackageChargeTicket(tx *sql.Tx, o Order) (string, bool, error) {
	pkg, err := readProjectPackage(tx, o.Project, true)
	if err != nil || pkg == nil || pkg.ChargedTicket != "" {
		return "", false, err
	}
	var ticketID string
	err = tx.QueryRow("SELECT id FROM project_tickets WHERE project_id=? AND slot=1 FOR UPDATE", o.Project).Scan(&ticketID)
	if err == sql.ErrNoRows {
		ticketID, err = normalProjectTicket(tx, o, 1)
	}
	if err != nil {
		return "", false, err
	}
	return ticketID, true, nil
}

func readProjectPackageView(tx *sql.Tx, projectID string) (*projectPackageView, error) {
	pkg, err := readProjectPackage(tx, projectID, false)
	if err != nil || pkg == nil {
		return nil, err
	}
	price := pkg.PriceCents
	return &projectPackageView{ID: pkg.PackageID, Name: pkg.Name, RoomCount: pkg.RoomCount, Price: &price, Coverage: pkg.Coverage}, nil
}

func readProjectPackageCharge(tx *sql.Tx, ticketID, projectID string) (*projectPackageChargeView, error) {
	query := `SELECT package_id,name,room_count,price_cents FROM project_packages WHERE charged_ticket_id=?`
	args := []any{ticketID}
	if ticketID == "" {
		query = `SELECT package_id,name,room_count,price_cents FROM project_packages WHERE project_id=? AND charged_ticket_id IS NOT NULL`
		args = []any{projectID}
	}
	var charge projectPackageChargeView
	err := tx.QueryRow(query, args...).Scan(&charge.ID, &charge.Name, &charge.RoomCount, &charge.Amount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &charge, nil
}

func (a *App) listCompanyPackages(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("orders") {
		return fail(403, "无制单权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	companyID := r.URL.Query().Get("company_id")
	if companyID == "" {
		return fail(400, "请选择公司")
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT seed_id,package_id,name,room_count,price_cents FROM catalog_seed_packages WHERE company_id=? AND classification='package' ORDER BY room_count,package_id`, companyID)
	if err != nil {
		return err
	}
	items := []companyPackageOption{}
	seedIDs := []string{}
	for rows.Next() {
		var item companyPackageOption
		var seedID string
		if err = rows.Scan(&seedID, &item.ID, &item.Name, &item.RoomCount, &item.Price); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
		seedIDs = append(seedIDs, seedID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for i := range items {
		items[i].Coverage, err = readCatalogPackageCoverage(tx, seedIDs[i], items[i].ID, companyID)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RoomCount != items[j].RoomCount {
			return items[i].RoomCount < items[j].RoomCount
		}
		return items[i].ID < items[j].ID
	})
	write(w, 200, map[string]any{"rows": items})
	return nil
}

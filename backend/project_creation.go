package main

import (
	"database/sql"
	"strings"
	"time"
)

type createdProject struct {
	ID, Number, Name, Address, TicketMode string
	Package                             *projectPackageSnapshot
}

func projectFromAddress(company, address string) (Project, error) {
	address = strings.TrimSpace(address)
	company = strings.TrimSpace(company)
	runes := []rune(address)
	if company == "" || len(runes) == 0 || len(runes) > 300 {
		return Project{}, fail(400, "请选择公司并填写1至300字的施工地址")
	}
	name := runes
	if len(name) > 200 {
		name = name[:200]
	}
	return Project{Company: company, Name: string(name), Address: address}, nil
}

// All project creates and address changes lock the company before any project.
// Existing order/ticket paths only lock their project; they must not acquire
// this company lock afterwards, which would reverse the lock order.
func lockProjectCompany(tx *sql.Tx, company string) error {
	var existing string
	if err := tx.QueryRow("SELECT id FROM companies WHERE id=? FOR UPDATE", company).Scan(&existing); err != nil {
		if err == sql.ErrNoRows {
			return fail(400, "关联公司不存在，请刷新后重新选择")
		}
		return err
	}
	return nil
}

func checkProjectAddress(tx *sql.Tx, company, address, exceptID string) error {
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM projects WHERE company_id=? AND BINARY address=BINARY ? AND id<>?", company, address, exceptID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fail(409, "该公司已有此施工地址，请在制单中查找并选择原地址；已停用或结项的工地请先到“工地汇总”重新启用")
	}
	return nil
}

// The caller owns the transaction. Creating a project through either menu or
// first order shares the snapshot and audit, and rolls back with that operation.
func createProject(tx *sql.Tx, u User, p Project) (createdProject, error) {
	var created createdProject
	if err := lockProjectCompany(tx, p.Company); err != nil {
		return created, err
	}
	if err := checkProjectAddress(tx, p.Company, p.Address, ""); err != nil {
		return created, err
	}
	created.ID, created.Name, created.Address = id(), p.Name, p.Address
	created.Number = "XM-" + time.Now().Format("20060102") + "-" + created.ID[:12]
	if _, err := tx.Exec("INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES(?,?,?,?,?,?)", created.ID, created.Number, p.Company, p.Name, p.Address, u.ID); err != nil {
		return created, err
	}
	var err error
	created.TicketMode, err = ensureProjectTicketSnapshot(tx, created.ID, p.Company)
	if err != nil {
		return created, err
	}
	if packageID := strings.TrimSpace(p.PackageID); packageID != "" {
		if !u.can("orders") {
			return created, fail(403, "无制单权限")
		}
		if err = requireCustomerPrices(u); err != nil {
			return created, err
		}
		var pkg projectPackageSnapshot
		pkg, err = insertProjectPackage(tx, created.ID, p.Company, packageID, u.ID)
		if err != nil {
			return created, err
		}
		created.Package = &pkg
	}
	if err := audit(tx, u, "新增工地项目", created.ID, map[string]any{
		"before": nil,
		"after":  map[string]any{"number": created.Number, "company_id": p.Company, "name": p.Name, "address": p.Address, "status": "进行中", "version": 1, "package": created.Package},
	}); err != nil {
		return created, err
	}
	return created, nil
}

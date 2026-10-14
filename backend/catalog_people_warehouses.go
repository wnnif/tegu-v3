package main

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

type workerEntityInput struct {
	ID      string   `json:"id"`
	Version int64    `json:"version"`
	Name    string   `json:"name"`
	Phone   string   `json:"phone"`
	Crafts  []string `json:"crafts"`
}

type warehouseEntityInput struct {
	ID            string         `json:"id"`
	Version       int64          `json:"version"`
	Name          string         `json:"name"`
	WorkerID      string         `json:"worker_id"`
	Active        catalogBoolean `json:"active"`
	Category      string         `json:"category"`
	WarehouseType string         `json:"warehouse_type"`
	Note          string         `json:"note"`
	Brand         string         `json:"brand"`
	Spec          string         `json:"spec"`
}

// Catalog rows return active as a number; editor forms send a bool.
type catalogBoolean bool

func (v *catalogBoolean) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch value := raw.(type) {
	case bool:
		*v = catalogBoolean(value)
	case float64:
		*v = catalogBoolean(int64(value) != 0)
	case string:
		n, _ := strconv.ParseInt(value, 10, 64)
		*v = catalogBoolean(n != 0)
	default:
		*v = false
	}
	return nil
}

func validEntityName(name string) error {
	if name == "" || len([]rune(name)) > 200 {
		return fail(400, "请填写有效名称")
	}
	return nil
}

func entitySaved(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fail(409, "资料已被修改，请刷新后重试")
	}
	return nil
}

func saveWorkerEntity(tx *sql.Tx, u User, body []byte) (any, error) {
	var input workerEntityInput
	if err := decode(body, &input); err != nil {
		return nil, err
	}
	input.Name, input.Phone, input.ID = strings.TrimSpace(input.Name), strings.TrimSpace(input.Phone), strings.TrimSpace(input.ID)
	if err := validEntityName(input.Name); err != nil {
		return nil, err
	}
	if len([]rune(input.Name)) > 100 || len([]rune(input.Phone)) > 60 {
		return nil, fail(400, "姓名或电话过长")
	}
	allowed, err := parseWorkerCrafts(input.Crafts)
	if err != nil {
		return nil, err
	}
	fresh := input.ID == ""
	if fresh {
		input.ID = id()
	}
	var result sql.Result
	if fresh {
		result, err = tx.Exec("INSERT INTO workers(id,name,phone,crafts) VALUES(?,?,?,?)", input.ID, input.Name, input.Phone, jsonText(allowed))
	} else {
		result, err = tx.Exec("UPDATE workers SET name=?,phone=?,crafts=?,version=version+1 WHERE id=? AND version=?", input.Name, input.Phone, jsonText(allowed), input.ID, input.Version)
	}
	if err != nil {
		return nil, err
	}
	if err = entitySaved(result); err != nil {
		return nil, err
	}
	if fresh {
		warehouseID := id()
		if _, err = tx.Exec("INSERT INTO warehouses(id,name,worker_id,active,category,warehouse_type,note) VALUES(?,?,?,1,'施工人仓库','普通仓库','新增施工人时自动创建')", warehouseID, input.Name, input.ID); err != nil {
			return nil, err
		}
		if err = audit(tx, u, "新增施工人自动建仓", warehouseID, map[string]string{"name": input.Name, "worker_id": input.ID}); err != nil {
			return nil, err
		}
	}
	if err = audit(tx, u, "保存workers", input.ID, input); err != nil {
		return nil, err
	}
	return map[string]any{"id": input.ID, "ok": true}, nil
}

func saveWarehouseEntity(tx *sql.Tx, u User, body []byte) (any, error) {
	var input warehouseEntityInput
	if err := decode(body, &input); err != nil {
		return nil, err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	input.WorkerID = strings.TrimSpace(input.WorkerID)
	input.Category = strings.TrimSpace(input.Category)
	input.WarehouseType = strings.TrimSpace(input.WarehouseType)
	input.Note = strings.TrimSpace(input.Note)
	if err := validEntityName(input.Name); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		value string
		limit int
	}{{input.Brand, 100}, {input.Spec, 200}} {
		if len([]rune(field.value)) > field.limit {
			return nil, fail(400, "资料文字过长，请缩短后保存")
		}
	}
	if len([]rune(input.Category)) > 100 || len([]rune(input.Note)) > 1000 {
		return nil, fail(400, "资料文字过长，请缩短后保存")
	}
	if input.WarehouseType == "" {
		input.WarehouseType = "普通仓库"
	}
	if !contains([]string{"普通仓库", "工地仓库", "临时仓库"}, input.WarehouseType) {
		return nil, fail(400, "仓库类型无效")
	}
	var worker any
	if input.WorkerID != "" {
		if err := checkRef(tx, "workers", input.WorkerID); err != nil {
			return nil, err
		}
		worker = input.WorkerID
	}
	fresh := input.ID == ""
	if fresh {
		input.ID = id()
	}
	var result sql.Result
	var err error
	if fresh {
		result, err = tx.Exec("INSERT INTO warehouses(id,name,worker_id,active,category,warehouse_type,note) VALUES(?,?,?,?,?,?,?)", input.ID, input.Name, worker, bool(input.Active), input.Category, input.WarehouseType, input.Note)
	} else {
		result, err = tx.Exec("UPDATE warehouses SET name=?,worker_id=?,active=?,category=?,warehouse_type=?,note=?,version=version+1 WHERE id=? AND version=?", input.Name, worker, bool(input.Active), input.Category, input.WarehouseType, input.Note, input.ID, input.Version)
	}
	if err != nil {
		return nil, err
	}
	if err = entitySaved(result); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "保存warehouses", input.ID, input); err != nil {
		return nil, err
	}
	return map[string]any{"id": input.ID, "ok": true}, nil
}

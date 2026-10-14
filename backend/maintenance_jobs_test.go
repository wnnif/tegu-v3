package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMaintenanceJobLifecycleMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{ID: "maintenance-test-admin", Name: "admin", Role: "admin"}
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	var runs atomic.Int32
	run := func(jobID string) (any, error) {
		runs.Add(1)
		a.updateMaintenanceJob(jobID, "执行中", 1, 2)
		<-release
		return map[string]any{"count": 2}, nil
	}
	w := httptest.NewRecorder()
	if err := a.startMaintenanceJob(w, admin, "archive", map[string]any{"month": "2026-09"}, run); err != nil {
		t.Fatal(err)
	}
	var first maintenanceJob
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || first.ID == "" {
		t.Fatal("missing accepted job", err)
	}
	w = httptest.NewRecorder()
	if err := a.startMaintenanceJob(w, admin, "archive", map[string]any{"month": "2026-09"}, run); err != nil {
		t.Fatal(err)
	}
	var repeated maintenanceJob
	if err := json.Unmarshal(w.Body.Bytes(), &repeated); err != nil || repeated.ID != first.ID {
		t.Fatal("duplicate submission created another job", err)
	}
	var conflict failure
	if err := a.startMaintenanceJob(httptest.NewRecorder(), admin, "archive", map[string]any{"month": "2026-10"}, run); !errors.As(err, &conflict) || conflict.Code != 409 {
		t.Fatal("different active archive was not rejected", err)
	}
	if err := a.listMaintenanceJobs(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/maintenance/jobs?id="+first.ID, nil), User{ID: "other", Role: "employee", Permissions: []string{"orders"}}); !errors.As(err, &conflict) || conflict.Code != 403 {
		t.Fatal("another employee could inspect job", err)
	}
	finish()
	finished := maintenanceFaultWaitJob(t, a, w)
	if finished.Status != "完成" || runs.Load() != 1 {
		t.Fatalf("job completion or duplicate run incorrect: %s %d", finished.Status, runs.Load())
	}
	if _, err := a.db.Exec("INSERT INTO maintenance_jobs(id,kind,actor_id,request_hash,active_key,status,stage,updated_at) VALUES(?, 'archive', ?, ?, 'archive', '运行中', '执行中', UTC_TIMESTAMP()-INTERVAL 3 MINUTE)", id(), admin.ID, hash([]byte("stale"))); err != nil {
		t.Fatal(err)
	}
	if err := a.reconcileMaintenanceJobs(); err != nil {
		t.Fatal(err)
	}
	var interrupted int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM maintenance_jobs WHERE status='中断' AND active_key IS NULL").Scan(&interrupted); err != nil || interrupted != 1 {
		t.Fatal("stale job not marked interrupted", interrupted, err)
	}
	if _, err := a.db.Exec("INSERT INTO maintenance_jobs(id,kind,actor_id,request_hash,active_key,status,stage) VALUES(?, 'archive', ?, ?, 'archive', '运行中', '执行中')", id(), admin.ID, hash([]byte("ongoing"))); err != nil {
		t.Fatal(err)
	}
	pageRecorder := httptest.NewRecorder()
	if err := a.listMaintenanceJobs(pageRecorder, httptest.NewRequest("GET", "/api/maintenance/jobs?kind=archive&page=2", nil), admin); err != nil {
		t.Fatal(err)
	}
	var archivePage struct {
		Rows  []maintenanceJob `json:"rows"`
		Total int              `json:"total"`
		Page  int              `json:"page"`
	}
	if err := json.Unmarshal(pageRecorder.Body.Bytes(), &archivePage); err != nil || archivePage.Total != 3 || archivePage.Page != 2 || len(archivePage.Rows) != 0 {
		t.Fatal("archive pagination incorrect", archivePage, err)
	}
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	file, err := a.backup()
	if err != nil {
		t.Fatal(err)
	}
	counts, err := inspectSnapshot(filepath.Join(dir, file))
	if err != nil || counts["maintenance_jobs"] != 2 {
		t.Fatal("snapshot must preserve finished jobs but omit active work", counts, err)
	}
	restoreRecorder := httptest.NewRecorder()
	request := restoreRequest{File: file, Target: "invalid_target", Confirm: "invalid_target"}
	if err := a.maintenance(restoreRecorder, httptest.NewRequest("POST", "/api/maintenance/restore", nil), admin, []byte(jsonText(request))); err != nil {
		t.Fatal(err)
	}
	if restored := maintenanceFaultWaitJob(t, a, restoreRecorder); restored.Status != "失败" {
		t.Fatal("invalid restore target did not produce a failed task", restored.Status)
	}
}

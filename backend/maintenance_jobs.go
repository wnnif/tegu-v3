package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func (a *App) operationLock(kind string) (func(), error) {
	conn, e := a.db.Conn(context.Background())
	if e != nil {
		return nil, e
	}
	name := "tegu:" + kind + ":" + hash([]byte(env("MYSQL_DATABASE", "tegu_v3")))[:16]
	var acquired int
	if e = conn.QueryRowContext(context.Background(), "SELECT GET_LOCK(?,0)", name).Scan(&acquired); e != nil || acquired != 1 {
		conn.Close()
		if e != nil {
			return nil, e
		}
		return nil, fail(409, "同类维护操作正在执行")
	}
	return func() {
		if _, e := conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", name); e != nil {
			log.Printf("maintenance lock release: %v", e)
		}
		conn.Close()
	}, nil
}

type maintenanceJob struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ActorID     string `json:"-"`
	RequestHash string `json:"-"`
	Status      string `json:"status"`
	Stage       string `json:"stage"`
	Progress    int    `json:"progress"`
	Total       int    `json:"total"`
	Message     string `json:"message"`
	Result      any    `json:"result,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (a *App) reconcileMaintenanceJobs() error {
	stale, e := a.db.Query("SELECT id FROM maintenance_jobs WHERE status='运行中' AND updated_at<UTC_TIMESTAMP()-INTERVAL 2 MINUTE LIMIT 20")
	if e != nil {
		return e
	}
	var ids []string
	for stale.Next() {
		var id string
		if e = stale.Scan(&id); e != nil {
			stale.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = stale.Err()
	stale.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = a.db.Exec("UPDATE maintenance_jobs SET status='中断',stage='需要核对',message='服务在执行期间中断；请核对文件或目标库后再提交',active_key=NULL,updated_at=UTC_TIMESTAMP() WHERE id=? AND status='运行中' AND updated_at<UTC_TIMESTAMP()-INTERVAL 2 MINUTE", id); e != nil {
			return e
		}
	}
	return nil
}

func (a *App) maintenanceJob(id string) (maintenanceJob, error) {
	var j maintenanceJob
	var result sql.NullString
	e := a.db.QueryRow("SELECT id,kind,actor_id,request_hash,status,stage,progress,total,message,result,created_at,updated_at FROM maintenance_jobs WHERE id=?", id).Scan(&j.ID, &j.Kind, &j.ActorID, &j.RequestHash, &j.Status, &j.Stage, &j.Progress, &j.Total, &j.Message, &result, &j.CreatedAt, &j.UpdatedAt)
	if e == sql.ErrNoRows {
		return j, fail(404, "维护任务不存在")
	}
	if e != nil {
		return j, e
	}
	if result.Valid {
		if e = json.Unmarshal([]byte(result.String), &j.Result); e != nil {
			return j, e
		}
	}
	return j, nil
}

func canReadMaintenanceJob(u User, j maintenanceJob) bool {
	if j.Kind == "archive" {
		return len(archiveReadableKinds(u)) > 0 && (j.ActorID == u.ID || u.Role == "admin")
	}
	return u.can("backups")
}

func (a *App) listMaintenanceJobs(w http.ResponseWriter, r *http.Request, u User) error {
	if e := a.reconcileMaintenanceJobs(); e != nil {
		return e
	}
	q := r.URL.Query()
	if wanted := q.Get("id"); wanted != "" {
		if len(wanted) != 32 {
			return fail(400, "维护任务编号无效")
		}
		j, e := a.maintenanceJob(wanted)
		if e != nil {
			return e
		}
		if !canReadMaintenanceJob(u, j) {
			return fail(403, "无权查看该维护任务")
		}
		write(w, 200, j)
		return nil
	}
	kind := q.Get("kind")
	if kind != "archive" && kind != "backup" && kind != "restore" && kind != "all" {
		return fail(400, "请选择维护任务类型")
	}
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	if kind == "archive" {
		if len(archiveReadableKinds(u)) == 0 {
			return fail(403, "无权查看归档任务")
		}
	} else if !u.can("backups") {
		return fail(403, "仅系统管理员可查看备份任务")
	}
	query := " FROM maintenance_jobs WHERE 1=1"
	args := []any{}
	if kind == "all" {
		query += " AND kind IN ('backup','restore')"
	} else {
		query += " AND kind=?"
		args = append(args, kind)
	}
	if kind == "archive" && u.Role != "admin" {
		query += " AND actor_id=?"
		args = append(args, u.ID)
	}
	var total int64
	if e = a.db.QueryRow("SELECT COUNT(*)"+query, args...).Scan(&total); e != nil {
		return e
	}
	jobRows, e := a.db.Query("SELECT id"+query+" ORDER BY created_at DESC,id DESC LIMIT 100 OFFSET ?", append(args, (page-1)*100)...)
	if e != nil {
		return e
	}
	var ids []string
	for jobRows.Next() {
		var id string
		if e = jobRows.Scan(&id); e != nil {
			jobRows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = jobRows.Err()
	jobRows.Close()
	if e != nil {
		return e
	}
	out := make([]maintenanceJob, 0, len(ids))
	for _, id := range ids {
		j, e := a.maintenanceJob(id)
		if e != nil {
			return e
		}
		out = append(out, j)
	}
	write(w, 200, map[string]any{"rows": out, "total": total, "page": page})
	return nil
}

// The nullable unique active_key reserves one running job of each kind across API processes.
func (a *App) startMaintenanceJob(w http.ResponseWriter, u User, kind string, payload any, run func(string) (any, error)) error {
	if e := a.reconcileMaintenanceJobs(); e != nil {
		return e
	}
	requestHash := hash([]byte(jsonText(payload)))
	jobID := id()
	_, e := a.db.Exec("INSERT INTO maintenance_jobs(id,kind,actor_id,request_hash,active_key,status,stage) VALUES(?,?,?,?,?,'运行中','等待执行')", jobID, kind, u.ID, requestHash, kind)
	if e != nil {
		var duplicate *mysql.MySQLError
		if !errors.As(e, &duplicate) || duplicate.Number != 1062 {
			return e
		}
		var activeID string
		if e = a.db.QueryRow("SELECT id FROM maintenance_jobs WHERE active_key=?", kind).Scan(&activeID); e != nil {
			return e
		}
		j, e := a.maintenanceJob(activeID)
		if e != nil {
			return e
		}
		if j.ActorID != u.ID || j.RequestHash != requestHash {
			return fail(409, "同类维护任务正在执行，请在任务记录中核对状态")
		}
		write(w, http.StatusAccepted, j)
		return nil
	}
	go a.runMaintenanceJob(jobID, run)
	j, e := a.maintenanceJob(jobID)
	if e != nil {
		return e
	}
	write(w, http.StatusAccepted, j)
	return nil
}

func (a *App) updateMaintenanceJob(id, stage string, progress, total int) {
	e := retryJobWrite(func() error {
		_, err := a.db.Exec("UPDATE maintenance_jobs SET stage=?,progress=?,total=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND status='运行中'", stage, progress, total, id)
		return err
	})
	if e != nil {
		log.Printf("maintenance job %s progress: %v", id, e)
	}
}

func retryJobWrite(write func() error) error {
	for attempt := 0; attempt < 3; attempt++ {
		e := write()
		if e == nil {
			return nil
		}
		var sqlError *mysql.MySQLError
		if !errors.As(e, &sqlError) || (sqlError.Number != 1213 && sqlError.Number != 1205) || attempt == 2 {
			return e
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	return nil
}

func (a *App) runMaintenanceJob(id string, run func(string) (any, error)) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_, e := a.db.Exec("UPDATE maintenance_jobs SET updated_at=UTC_TIMESTAMP() WHERE id=? AND status='运行中'", id)
				if e != nil {
					log.Printf("maintenance job %s heartbeat: %v", id, e)
				}
			}
		}
	}()
	defer close(done)
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("maintenance job %s panic: %v", id, recovered)
			a.finishMaintenanceJob(id, nil, fail(500, "维护任务异常中断，请核对结果"))
		}
	}()
	result, e := run(id)
	a.finishMaintenanceJob(id, result, e)
}

func (a *App) finishMaintenanceJob(id string, result any, operationErr error) {
	status, stage, message := "完成", "已完成", ""
	if operationErr != nil {
		status, stage = "失败", "需核对"
		message = "维护任务失败，请核对结果后重试"
		var f failure
		if errors.As(operationErr, &f) {
			message = f.Message
		}
		log.Printf("maintenance job %s failed: %v", id, operationErr)
	}
	if len(message) > 1000 {
		message = strings.ToValidUTF8(message[:1000], "")
	}
	e := retryJobWrite(func() error {
		_, err := a.db.Exec("UPDATE maintenance_jobs SET status=?,stage=?,message=?,result=?,active_key=NULL,updated_at=UTC_TIMESTAMP() WHERE id=? AND status='运行中'", status, stage, message, jsonText(result), id)
		return err
	})
	if e != nil {
		log.Printf("maintenance job %s completion write failed: %v", id, e)
	}
}

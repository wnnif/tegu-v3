package main

import "database/sql"

type backupRunRecord struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
	Available bool   `json:"available"`
	Bytes     int64  `json:"bytes,omitempty"`
}

type backupListResponse struct {
	Rows      []backupRunRecord `json:"rows"`
	Total     int64             `json:"total"`
	Page      int               `json:"page"`
	UsedBytes int64             `json:"used_bytes"`
	FreeBytes *uint64           `json:"free_bytes,omitempty"`
	LowSpace  bool              `json:"low_space"`
}

type archiveRunRecord struct {
	ID        string        `json:"id"`
	Month     string        `json:"month"`
	Automatic bool          `json:"automatic"`
	CreatedAt string        `json:"created_at"`
	Files     []ArchiveFile `json:"files"`
	manifest  string
}

type archiveStatusRecord struct {
	Month string `json:"month"`
	Error string `json:"error"`
	Time  string `json:"time"`
}

type archiveListResponse struct {
	Rows   []archiveRunRecord   `json:"rows"`
	Total  int64                `json:"total"`
	Page   int                  `json:"page"`
	Latest *archiveStatusRecord `json:"latest"`
}

func scanBackupRuns(q rowQuery, page int) ([]backupRunRecord, error) {
	return scanRows[backupRunRecord](q, "SELECT id,file,status,message,created_at FROM backup_runs ORDER BY created_at DESC,id DESC LIMIT 100 OFFSET ?", []any{(page - 1) * 100}, func(r *sql.Rows, item *backupRunRecord) error {
		return r.Scan(&item.ID, &item.File, &item.Status, &item.Message, &item.CreatedAt)
	})
}

func scanArchiveRuns(q rowQuery, where string, args []any, page int) ([]archiveRunRecord, error) {
	return scanRows[archiveRunRecord](q, "SELECT id,month,automatic,manifest,created_at FROM archive_runs"+where+" ORDER BY created_at DESC,id DESC LIMIT 100 OFFSET ?", append(args, (page-1)*100), func(r *sql.Rows, item *archiveRunRecord) error {
		return r.Scan(&item.ID, &item.Month, &item.Automatic, &item.manifest, &item.CreatedAt)
	})
}

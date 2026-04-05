package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

type APIKey struct {
	ID        int64
	Key       string
	Email     string
	Plan      string // "free", "starter", "pro"
	UsedCalls int64
	MaxCalls  int64
	CreatedAt time.Time
}

type UsageLog struct {
	ID        int64
	APIKeyID  int64
	Endpoint  string
	Status    int
	LatencyMs int64
	CreatedAt time.Time
}

type BatchJob struct {
	ID         string
	APIKeyID   int64
	Status     string // "pending", "processing", "completed", "failed"
	FileCount  int
	Completed  int
	WebhookURL string
	Results    json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type DailyUsage struct {
	Date  string `json:"date"`
	Calls int64  `json:"calls"`
}

func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	if err := conn.Ping(); err != nil {
		return nil, err
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}

	return db, nil
}

func (d *DB) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			email TEXT NOT NULL,
			plan TEXT NOT NULL DEFAULT 'free',
			used_calls INTEGER NOT NULL DEFAULT 0,
			max_calls INTEGER NOT NULL DEFAULT 100,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS usage_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key_id INTEGER NOT NULL,
			endpoint TEXT NOT NULL,
			status INTEGER NOT NULL,
			latency_ms INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (api_key_id) REFERENCES api_keys(id)
		)`,
		`CREATE TABLE IF NOT EXISTS batch_jobs (
			id TEXT PRIMARY KEY,
			api_key_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			file_count INTEGER NOT NULL DEFAULT 0,
			completed INTEGER NOT NULL DEFAULT 0,
			webhook_url TEXT DEFAULT '',
			results TEXT DEFAULT '[]',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (api_key_id) REFERENCES api_keys(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_key ON api_keys(key)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_logs_api_key_id ON usage_logs(api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_logs_created_at ON usage_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_batch_jobs_api_key_id ON batch_jobs(api_key_id)`,
	}

	for _, q := range queries {
		if _, err := d.conn.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) CreateAPIKey(key, email, plan string, maxCalls int64) (*APIKey, error) {
	result, err := d.conn.Exec(
		"INSERT INTO api_keys (key, email, plan, max_calls) VALUES (?, ?, ?, ?)",
		key, email, plan, maxCalls,
	)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	return &APIKey{
		ID:       id,
		Key:      key,
		Email:    email,
		Plan:     plan,
		MaxCalls: maxCalls,
	}, nil
}

func (d *DB) GetAPIKey(key string) (*APIKey, error) {
	ak := &APIKey{}
	err := d.conn.QueryRow(
		"SELECT id, key, email, plan, used_calls, max_calls, created_at FROM api_keys WHERE key = ?",
		key,
	).Scan(&ak.ID, &ak.Key, &ak.Email, &ak.Plan, &ak.UsedCalls, &ak.MaxCalls, &ak.CreatedAt)
	if err != nil {
		return nil, err
	}
	return ak, nil
}

func (d *DB) IncrementUsage(apiKeyID int64) error {
	_, err := d.conn.Exec("UPDATE api_keys SET used_calls = used_calls + 1 WHERE id = ?", apiKeyID)
	return err
}

func (d *DB) IncrementUsageBy(apiKeyID int64, n int) error {
	_, err := d.conn.Exec("UPDATE api_keys SET used_calls = used_calls + ? WHERE id = ?", n, apiKeyID)
	return err
}

func (d *DB) LogUsage(apiKeyID int64, endpoint string, status int, latencyMs int64) error {
	_, err := d.conn.Exec(
		"INSERT INTO usage_logs (api_key_id, endpoint, status, latency_ms) VALUES (?, ?, ?, ?)",
		apiKeyID, endpoint, status, latencyMs,
	)
	return err
}

func (d *DB) GetUsageStats(apiKeyID int64) (todayCalls int64, monthCalls int64, err error) {
	err = d.conn.QueryRow(
		"SELECT COUNT(*) FROM usage_logs WHERE api_key_id = ? AND date(created_at) = date('now')",
		apiKeyID,
	).Scan(&todayCalls)
	if err != nil {
		return
	}

	err = d.conn.QueryRow(
		"SELECT COUNT(*) FROM usage_logs WHERE api_key_id = ? AND created_at >= date('now', 'start of month')",
		apiKeyID,
	).Scan(&monthCalls)
	return
}

func (d *DB) GetDailyUsage(apiKeyID int64, days int) ([]DailyUsage, error) {
	rows, err := d.conn.Query(
		`SELECT date(created_at) as day, COUNT(*) as calls
		 FROM usage_logs
		 WHERE api_key_id = ? AND created_at >= date('now', ?)
		 GROUP BY day ORDER BY day`,
		apiKeyID, fmt.Sprintf("-%d days", days),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var usage []DailyUsage
	for rows.Next() {
		var u DailyUsage
		if err := rows.Scan(&u.Date, &u.Calls); err != nil {
			return nil, err
		}
		usage = append(usage, u)
	}
	return usage, rows.Err()
}

func (d *DB) GetRecentLogs(apiKeyID int64, limit int) ([]UsageLog, error) {
	rows, err := d.conn.Query(
		`SELECT id, api_key_id, endpoint, status, latency_ms, created_at
		 FROM usage_logs WHERE api_key_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		apiKeyID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []UsageLog
	for rows.Next() {
		var l UsageLog
		if err := rows.Scan(&l.ID, &l.APIKeyID, &l.Endpoint, &l.Status, &l.LatencyMs, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func (d *DB) GetAPIKeyByEmail(email string) (*APIKey, error) {
	ak := &APIKey{}
	err := d.conn.QueryRow(
		"SELECT id, key, email, plan, used_calls, max_calls, created_at FROM api_keys WHERE email = ? ORDER BY created_at DESC LIMIT 1",
		email,
	).Scan(&ak.ID, &ak.Key, &ak.Email, &ak.Plan, &ak.UsedCalls, &ak.MaxCalls, &ak.CreatedAt)
	if err != nil {
		return nil, err
	}
	return ak, nil
}

func (d *DB) UpgradePlan(apiKeyID int64, plan string, maxCalls int64) error {
	_, err := d.conn.Exec(
		"UPDATE api_keys SET plan = ?, max_calls = ?, used_calls = 0 WHERE id = ?",
		plan, maxCalls, apiKeyID,
	)
	return err
}

func (d *DB) ResetMonthlyUsage() error {
	_, err := d.conn.Exec("UPDATE api_keys SET used_calls = 0")
	return err
}

// Batch job methods

func (d *DB) CreateBatchJob(id string, apiKeyID int64, fileCount int, webhookURL string) (*BatchJob, error) {
	_, err := d.conn.Exec(
		"INSERT INTO batch_jobs (id, api_key_id, status, file_count, webhook_url) VALUES (?, ?, 'pending', ?, ?)",
		id, apiKeyID, fileCount, webhookURL,
	)
	if err != nil {
		return nil, err
	}
	return &BatchJob{
		ID:         id,
		APIKeyID:   apiKeyID,
		Status:     "pending",
		FileCount:  fileCount,
		WebhookURL: webhookURL,
	}, nil
}

func (d *DB) GetBatchJob(id string, apiKeyID int64) (*BatchJob, error) {
	job := &BatchJob{}
	var results string
	err := d.conn.QueryRow(
		"SELECT id, api_key_id, status, file_count, completed, webhook_url, results, created_at, updated_at FROM batch_jobs WHERE id = ? AND api_key_id = ?",
		id, apiKeyID,
	).Scan(&job.ID, &job.APIKeyID, &job.Status, &job.FileCount, &job.Completed, &job.WebhookURL, &results, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	job.Results = json.RawMessage(results)
	return job, nil
}

func (d *DB) UpdateBatchJob(id, status string, completed int, results json.RawMessage) error {
	_, err := d.conn.Exec(
		"UPDATE batch_jobs SET status = ?, completed = ?, results = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		status, completed, string(results), id,
	)
	return err
}

// StartMonthlyResetJob runs a background goroutine that resets usage counters
// at the start of each month.
func (d *DB) StartMonthlyResetJob() {
	go func() {
		for {
			now := time.Now().UTC()
			nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			sleepDuration := nextMonth.Sub(now)

			slog.Info("monthly usage reset scheduled", "next_reset", nextMonth.Format(time.RFC3339))
			time.Sleep(sleepDuration)

			if err := d.ResetMonthlyUsage(); err != nil {
				slog.Error("failed to reset monthly usage", "error", err)
			} else {
				slog.Info("monthly usage counters reset")
			}
		}
	}()
}

func (d *DB) RotateAPIKey(apiKeyID int64, newKey string) error {
	_, err := d.conn.Exec("UPDATE api_keys SET key = ? WHERE id = ?", newKey, apiKeyID)
	return err
}

func (d *DB) Close() error {
	return d.conn.Close()
}


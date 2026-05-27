package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite connection.
type DB struct {
	conn *sql.DB
}

// User represents a portal user.
type User struct {
	ID            int64      `json:"id"`
	Username      string     `json:"username"`
	Email         string     `json:"email"`
	PasswordHash  string     `json:"-"`
	Role          string     `json:"role"`
	AppriseAPIURL string     `json:"appriseApiUrl"`
	DefaultURLs   string     `json:"defaultUrls"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// AlertRule is a user-configured notification rule.
type AlertRule struct {
	ID              int64      `json:"id"`
	UserID          int64      `json:"userId"`
	Name            string     `json:"name"`
	EventType       string     `json:"eventType"`
	Conditions      string     `json:"conditions"`    // JSON blob
	AppriseURLs     string     `json:"appriseUrls"`   // newline-separated
	Enabled         bool       `json:"enabled"`
	CooldownMinutes int        `json:"cooldownMinutes"`
	LastFired       *time.Time `json:"lastFired"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// AlertLog records each time a notification fires.
type AlertLog struct {
	ID          int64     `json:"id"`
	AlertRuleID int64     `json:"alertRuleId"`
	UserID      int64     `json:"userId"`
	RuleName    string    `json:"ruleName"`
	Message     string    `json:"message"`
	FiredAt     time.Time `json:"firedAt"`
}

// New opens (or creates) the SQLite database and runs migrations.
func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite: single writer

	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error { return d.conn.Close() }

func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS users (
    id             INTEGER  PRIMARY KEY AUTOINCREMENT,
    username       TEXT     UNIQUE NOT NULL,
    email          TEXT     UNIQUE NOT NULL,
    password_hash  TEXT     NOT NULL,
    role           TEXT     NOT NULL DEFAULT 'user',
    apprise_api_url TEXT    NOT NULL DEFAULT '',
    default_urls   TEXT     NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS alert_rules (
    id               INTEGER  PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name             TEXT     NOT NULL,
    event_type       TEXT     NOT NULL,
    conditions       TEXT     NOT NULL DEFAULT '{}',
    apprise_urls     TEXT     NOT NULL DEFAULT '',
    enabled          INTEGER  NOT NULL DEFAULT 1,
    cooldown_minutes INTEGER  NOT NULL DEFAULT 5,
    last_fired       DATETIME,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS alert_log (
    id             INTEGER  PRIMARY KEY AUTOINCREMENT,
    alert_rule_id  INTEGER  NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    user_id        INTEGER  NOT NULL,
    rule_name      TEXT     NOT NULL,
    message        TEXT     NOT NULL,
    fired_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scheduler_state (
    key        TEXT     PRIMARY KEY,
    value      TEXT     NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	return err
}

// ─── Users ──────────────────────────────────────────────────────────────────

func (d *DB) CountUsers() (int, error) {
	var n int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

func (d *DB) CreateUser(username, email, passwordHash, role string) (*User, error) {
	res, err := d.conn.Exec(
		"INSERT INTO users (username, email, password_hash, role) VALUES (?, ?, ?, ?)",
		username, email, passwordHash, role,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetUserByID(id)
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := d.conn.QueryRow(
		"SELECT id, username, email, password_hash, role, apprise_api_url, default_urls, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.AppriseAPIURL, &u.DefaultURLs, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := d.conn.QueryRow(
		"SELECT id, username, email, password_hash, role, apprise_api_url, default_urls, created_at FROM users WHERE username = ?", username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.AppriseAPIURL, &u.DefaultURLs, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) GetAllUsers() ([]*User, error) {
	rows, err := d.conn.Query(
		"SELECT id, username, email, password_hash, role, apprise_api_url, default_urls, created_at FROM users ORDER BY created_at",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.AppriseAPIURL, &u.DefaultURLs, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) UpdateUserSettings(userID int64, appriseAPIURL, defaultURLs string) error {
	_, err := d.conn.Exec(
		"UPDATE users SET apprise_api_url = ?, default_urls = ? WHERE id = ?",
		appriseAPIURL, defaultURLs, userID,
	)
	return err
}

func (d *DB) UpdateUserPassword(userID int64, passwordHash string) error {
	_, err := d.conn.Exec("UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, userID)
	return err
}

func (d *DB) UpdateUserRole(userID int64, role string) error {
	_, err := d.conn.Exec("UPDATE users SET role = ? WHERE id = ?", role, userID)
	return err
}

func (d *DB) DeleteUser(id int64) error {
	_, err := d.conn.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

// ─── Alert Rules ─────────────────────────────────────────────────────────────

func (d *DB) CreateAlertRule(rule *AlertRule) (*AlertRule, error) {
	res, err := d.conn.Exec(
		`INSERT INTO alert_rules (user_id, name, event_type, conditions, apprise_urls, enabled, cooldown_minutes)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rule.UserID, rule.Name, rule.EventType, rule.Conditions,
		rule.AppriseURLs, rule.Enabled, rule.CooldownMinutes,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetAlertRule(id, rule.UserID)
}

func (d *DB) GetAlertRule(id, userID int64) (*AlertRule, error) {
	r := &AlertRule{}
	err := d.conn.QueryRow(
		`SELECT id, user_id, name, event_type, conditions, apprise_urls, enabled, cooldown_minutes, last_fired, created_at
		 FROM alert_rules WHERE id = ? AND user_id = ?`, id, userID,
	).Scan(&r.ID, &r.UserID, &r.Name, &r.EventType, &r.Conditions, &r.AppriseURLs,
		&r.Enabled, &r.CooldownMinutes, &r.LastFired, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func (d *DB) GetUserAlertRules(userID int64) ([]*AlertRule, error) {
	rows, err := d.conn.Query(
		`SELECT id, user_id, name, event_type, conditions, apprise_urls, enabled, cooldown_minutes, last_fired, created_at
		 FROM alert_rules WHERE user_id = ? ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []*AlertRule
	for rows.Next() {
		r := &AlertRule{}
		if err := rows.Scan(&r.ID, &r.UserID, &r.Name, &r.EventType, &r.Conditions,
			&r.AppriseURLs, &r.Enabled, &r.CooldownMinutes, &r.LastFired, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (d *DB) GetAllEnabledAlertRules() ([]*AlertRule, error) {
	rows, err := d.conn.Query(
		`SELECT id, user_id, name, event_type, conditions, apprise_urls, enabled, cooldown_minutes, last_fired, created_at
		 FROM alert_rules WHERE enabled = 1`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []*AlertRule
	for rows.Next() {
		r := &AlertRule{}
		if err := rows.Scan(&r.ID, &r.UserID, &r.Name, &r.EventType, &r.Conditions,
			&r.AppriseURLs, &r.Enabled, &r.CooldownMinutes, &r.LastFired, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (d *DB) UpdateAlertRule(rule *AlertRule) error {
	_, err := d.conn.Exec(
		`UPDATE alert_rules SET name=?, event_type=?, conditions=?, apprise_urls=?, enabled=?, cooldown_minutes=?
		 WHERE id=? AND user_id=?`,
		rule.Name, rule.EventType, rule.Conditions, rule.AppriseURLs,
		rule.Enabled, rule.CooldownMinutes, rule.ID, rule.UserID,
	)
	return err
}

func (d *DB) UpdateAlertRuleLastFired(id int64, t time.Time) error {
	_, err := d.conn.Exec("UPDATE alert_rules SET last_fired=? WHERE id=?", t, id)
	return err
}

func (d *DB) DeleteAlertRule(id, userID int64) error {
	_, err := d.conn.Exec("DELETE FROM alert_rules WHERE id=? AND user_id=?", id, userID)
	return err
}

// ─── Alert Log ───────────────────────────────────────────────────────────────

func (d *DB) CreateAlertLog(entry *AlertLog) error {
	_, err := d.conn.Exec(
		"INSERT INTO alert_log (alert_rule_id, user_id, rule_name, message) VALUES (?, ?, ?, ?)",
		entry.AlertRuleID, entry.UserID, entry.RuleName, entry.Message,
	)
	return err
}

func (d *DB) GetUserAlertLog(userID int64, limit int) ([]*AlertLog, error) {
	rows, err := d.conn.Query(
		`SELECT id, alert_rule_id, user_id, rule_name, message, fired_at
		 FROM alert_log WHERE user_id = ? ORDER BY fired_at DESC LIMIT ?`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []*AlertLog
	for rows.Next() {
		l := &AlertLog{}
		if err := rows.Scan(&l.ID, &l.AlertRuleID, &l.UserID, &l.RuleName, &l.Message, &l.FiredAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// ─── Scheduler State ─────────────────────────────────────────────────────────

func (d *DB) GetState(key string) (string, error) {
	var v string
	err := d.conn.QueryRow("SELECT value FROM scheduler_state WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (d *DB) SetState(key, value string) error {
	_, err := d.conn.Exec(
		`INSERT INTO scheduler_state (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value,
	)
	return err
}

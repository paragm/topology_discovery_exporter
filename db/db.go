package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// DB wraps the SQLite database connection.
type DB struct {
	db *sql.DB
}

// Change represents a recorded topology change event.
type Change struct {
	ID         int64
	Timestamp  time.Time
	ChangeType string
	Entity     string
	Details    string
}

// Open opens or creates the SQLite database with WAL mode enabled.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for concurrent read/write
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	// Set reasonable connection pool
	sqlDB.SetMaxOpenConns(1) // SQLite supports one writer
	sqlDB.SetMaxIdleConns(1)

	return &DB{db: sqlDB}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// InitSchema creates the database tables if they don't exist.
func (d *DB) InitSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS switches (
		name       TEXT PRIMARY KEY,
		ip         TEXT NOT NULL,
		role       TEXT NOT NULL,
		sys_desc   TEXT DEFAULT '',
		uplink_to  TEXT DEFAULT '',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS switch_ports (
		switch_name TEXT NOT NULL,
		port_id     TEXT NOT NULL,
		port_desc   TEXT DEFAULT '',
		oper_status TEXT DEFAULT 'unknown',
		connected   TEXT DEFAULT '',
		updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (switch_name, port_id),
		FOREIGN KEY (switch_name) REFERENCES switches(name)
	);

	CREATE TABLE IF NOT EXISTS host_connections (
		hostname          TEXT PRIMARY KEY,
		switch_id         TEXT NOT NULL,
		switch_port       TEXT NOT NULL,
		switch_port_index TEXT DEFAULT '',
		uplink_switch_l1  TEXT DEFAULT '',
		uplink_port_l1    TEXT DEFAULT '',
		uplink_switch_l2  TEXT DEFAULT '',
		uplink_port_l2    TEXT DEFAULT '',
		network_path      TEXT DEFAULT '',
		topology_source   TEXT DEFAULT 'lldp',
		updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS topology_history (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP,
		change_type TEXT NOT NULL,
		entity      TEXT NOT NULL,
		details     TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS discovery_runs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		started_at DATETIME NOT NULL,
		duration   REAL NOT NULL,
		success    BOOLEAN NOT NULL,
		switches   INTEGER DEFAULT 0,
		hosts      INTEGER DEFAULT 0,
		links      INTEGER DEFAULT 0,
		errors     TEXT DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_topology_history_timestamp ON topology_history(timestamp);
	CREATE INDEX IF NOT EXISTS idx_discovery_runs_started ON discovery_runs(started_at);
	`

	_, err := d.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}

// Switch represents a switch record for database operations.
type Switch struct {
	Name     string
	IP       string
	Role     string
	SysDesc  string
	UplinkTo string
}

// HostConnection represents a host connection record for database operations.
type HostConnection struct {
	Hostname        string
	SwitchID        string
	SwitchPort      string
	SwitchPortIndex string
	UplinkSwitchL1  string
	UplinkPortL1    string
	UplinkSwitchL2  string
	UplinkPortL2    string
	NetworkPath     string
	TopologySource  string
}

// Link represents a link record for database operations.
type Link struct {
	From         string
	FromPort     string
	FromPortDesc string
	To           string
	ToPort       string
	LinkType     string
	OperStatus   string
}

// SaveDiscovery persists the current discovery results to the database.
func (d *DB) SaveDiscovery(switches []Switch, hosts []HostConnection, links []Link) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	// Upsert switches
	switchStmt, err := tx.Prepare(`
		INSERT INTO switches (name, ip, role, sys_desc, uplink_to, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			ip = excluded.ip,
			role = excluded.role,
			sys_desc = excluded.sys_desc,
			uplink_to = excluded.uplink_to,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare switch statement: %w", err)
	}
	defer switchStmt.Close()

	for _, sw := range switches {
		if _, err := switchStmt.Exec(sw.Name, sw.IP, sw.Role, sw.SysDesc, sw.UplinkTo); err != nil {
			return fmt.Errorf("upsert switch %s: %w", sw.Name, err)
		}
	}

	// Upsert host connections
	hostStmt, err := tx.Prepare(`
		INSERT INTO host_connections (hostname, switch_id, switch_port, switch_port_index,
			uplink_switch_l1, uplink_port_l1, uplink_switch_l2, uplink_port_l2,
			network_path, topology_source, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(hostname) DO UPDATE SET
			switch_id = excluded.switch_id,
			switch_port = excluded.switch_port,
			switch_port_index = excluded.switch_port_index,
			uplink_switch_l1 = excluded.uplink_switch_l1,
			uplink_port_l1 = excluded.uplink_port_l1,
			uplink_switch_l2 = excluded.uplink_switch_l2,
			uplink_port_l2 = excluded.uplink_port_l2,
			network_path = excluded.network_path,
			topology_source = excluded.topology_source,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare host statement: %w", err)
	}
	defer hostStmt.Close()

	for _, h := range hosts {
		if _, err := hostStmt.Exec(h.Hostname, h.SwitchID, h.SwitchPort, h.SwitchPortIndex,
			h.UplinkSwitchL1, h.UplinkPortL1, h.UplinkSwitchL2, h.UplinkPortL2,
			h.NetworkPath, h.TopologySource); err != nil {
			return fmt.Errorf("upsert host %s: %w", h.Hostname, err)
		}
	}

	// Upsert switch ports from links
	portStmt, err := tx.Prepare(`
		INSERT INTO switch_ports (switch_name, port_id, port_desc, oper_status, connected, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(switch_name, port_id) DO UPDATE SET
			port_desc = excluded.port_desc,
			oper_status = excluded.oper_status,
			connected = excluded.connected,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare port statement: %w", err)
	}
	defer portStmt.Close()

	for _, l := range links {
		if l.From != "" {
			if _, err := portStmt.Exec(l.From, l.FromPort, l.FromPortDesc, l.OperStatus, l.To); err != nil {
				return fmt.Errorf("upsert port %s/%s: %w", l.From, l.FromPort, err)
			}
		}
	}

	return tx.Commit()
}

// GetLastDiscovery retrieves the most recent discovery data from the database.
func (d *DB) GetLastDiscovery() ([]Switch, []HostConnection, []Link, error) {
	// Load switches
	rows, err := d.db.Query("SELECT name, ip, role, sys_desc, uplink_to FROM switches")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query switches: %w", err)
	}
	defer rows.Close()

	var switches []Switch
	for rows.Next() {
		var sw Switch
		if err := rows.Scan(&sw.Name, &sw.IP, &sw.Role, &sw.SysDesc, &sw.UplinkTo); err != nil {
			return nil, nil, nil, fmt.Errorf("scan switch: %w", err)
		}
		switches = append(switches, sw)
	}

	// Load host connections
	rows, err = d.db.Query(`SELECT hostname, switch_id, switch_port, switch_port_index,
		uplink_switch_l1, uplink_port_l1, uplink_switch_l2, uplink_port_l2,
		network_path, topology_source FROM host_connections`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query hosts: %w", err)
	}
	defer rows.Close()

	var hosts []HostConnection
	for rows.Next() {
		var h HostConnection
		if err := rows.Scan(&h.Hostname, &h.SwitchID, &h.SwitchPort, &h.SwitchPortIndex,
			&h.UplinkSwitchL1, &h.UplinkPortL1, &h.UplinkSwitchL2, &h.UplinkPortL2,
			&h.NetworkPath, &h.TopologySource); err != nil {
			return nil, nil, nil, fmt.Errorf("scan host: %w", err)
		}
		hosts = append(hosts, h)
	}

	// Load links from switch_ports
	rows, err = d.db.Query("SELECT switch_name, port_id, port_desc, oper_status, connected FROM switch_ports")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query ports: %w", err)
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.From, &l.FromPort, &l.FromPortDesc, &l.OperStatus, &l.To); err != nil {
			return nil, nil, nil, fmt.Errorf("scan port: %w", err)
		}
		links = append(links, l)
	}

	return switches, hosts, links, nil
}

// RecordChange records a topology change event.
func (d *DB) RecordChange(changeType, entity, details string) error {
	_, err := d.db.Exec(
		"INSERT INTO topology_history (change_type, entity, details) VALUES (?, ?, ?)",
		changeType, entity, details,
	)
	if err != nil {
		return fmt.Errorf("record change: %w", err)
	}
	return nil
}

// GetRecentChanges retrieves topology changes since the given time.
func (d *DB) GetRecentChanges(since time.Time) ([]Change, error) {
	rows, err := d.db.Query(
		"SELECT id, timestamp, change_type, entity, details FROM topology_history WHERE timestamp >= ? ORDER BY timestamp DESC",
		since,
	)
	if err != nil {
		return nil, fmt.Errorf("query changes: %w", err)
	}
	defer rows.Close()

	var changes []Change
	for rows.Next() {
		var c Change
		var ts string
		if err := rows.Scan(&c.ID, &ts, &c.ChangeType, &c.Entity, &c.Details); err != nil {
			return nil, fmt.Errorf("scan change: %w", err)
		}
		c.Timestamp, _ = time.Parse("2006-01-02 15:04:05", ts)
		changes = append(changes, c)
	}
	return changes, nil
}

package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "github.com/mattn/go-sqlite3"

	"xns/pkg/xns"
)

type Snapshot struct {
	Height          uint64
	BlockHash       string
	ProtocolAddress string
	Names           map[string]xns.Entry
	Events          []xns.Event
}

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=FULL&_foreign_keys=ON")
	if err != nil {
		return nil, err
	}
	s := &DB{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS names (
	name TEXT PRIMARY KEY,
	owner_key TEXT NOT NULL,
	expiration_height INTEGER NOT NULL,
	first_claim_height INTEGER NOT NULL,
	last_update_height INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS name_txids (
	name TEXT NOT NULL,
	txid TEXT NOT NULL,
	position INTEGER NOT NULL,
	PRIMARY KEY (name, txid),
	FOREIGN KEY (name) REFERENCES names(name) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS events (
	txid TEXT PRIMARY KEY,
	height INTEGER NOT NULL,
	amount_atomic INTEGER NOT NULL,
	years INTEGER NOT NULL,
	name TEXT,
	owner_key TEXT,
	action TEXT NOT NULL,
	reason TEXT,
	previous_expiration_height INTEGER,
	expiration_height INTEGER
);
`)
	return err
}

func (s *DB) Load() (Snapshot, error) {
	snap := Snapshot{Names: make(map[string]xns.Entry)}
	var heightText string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'height'`).Scan(&heightText)
	if err == nil {
		height, err := strconv.ParseUint(heightText, 10, 64)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Height = height
	} else if err != sql.ErrNoRows {
		return Snapshot{}, err
	}
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'protocol_address'`).Scan(&snap.ProtocolAddress); err != nil && err != sql.ErrNoRows {
		return Snapshot{}, err
	}
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'block_hash'`).Scan(&snap.BlockHash); err != nil && err != sql.ErrNoRows {
		return Snapshot{}, err
	}

	rows, err := s.db.Query(`
SELECT name, owner_key, expiration_height, first_claim_height, last_update_height
FROM names
ORDER BY name`)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e xns.Entry
		if err := rows.Scan(&e.Name, &e.OwnerKey, &e.ExpirationHeight, &e.FirstClaimHeight, &e.LastUpdateHeight); err != nil {
			return Snapshot{}, err
		}
		txids, err := s.txids(e.Name)
		if err != nil {
			return Snapshot{}, err
		}
		e.SourceTxIDs = txids
		snap.Names[e.Name] = e
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}

	events, err := s.events()
	if err != nil {
		return Snapshot{}, err
	}
	snap.Events = events
	return snap, nil
}

func (s *DB) txids(name string) ([]string, error) {
	rows, err := s.db.Query(`SELECT txid FROM name_txids WHERE name = ? ORDER BY position`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var txid string
		if err := rows.Scan(&txid); err != nil {
			return nil, err
		}
		out = append(out, txid)
	}
	return out, rows.Err()
}

func (s *DB) events() ([]xns.Event, error) {
	rows, err := s.db.Query(`
SELECT txid, height, amount_atomic, years, name, owner_key, action, reason,
       previous_expiration_height, expiration_height
FROM events
ORDER BY height, txid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []xns.Event
	for rows.Next() {
		var e xns.Event
		var name, owner, reason sql.NullString
		var prev, exp sql.NullInt64
		if err := rows.Scan(&e.TxID, &e.Height, &e.Amount, &e.Years, &name, &owner, &e.Action, &reason, &prev, &exp); err != nil {
			return nil, err
		}
		e.Name = name.String
		e.OwnerKey = owner.String
		e.Reason = reason.String
		if prev.Valid {
			e.PreviousExpirationHeight = uint64(prev.Int64)
		}
		if exp.Valid {
			e.ExpirationHeight = uint64(exp.Int64)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *DB) Save(snap Snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback()
		}
	}()
	for _, stmt := range []string{
		`DELETE FROM name_txids`,
		`DELETE FROM names`,
		`DELETE FROM events`,
		`DELETE FROM meta`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('height', ?)`, strconv.FormatUint(snap.Height, 10)); err != nil {
		return err
	}
	if snap.BlockHash != "" {
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('block_hash', ?)`, snap.BlockHash); err != nil {
			return err
		}
	}
	if snap.ProtocolAddress != "" {
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('protocol_address', ?)`, snap.ProtocolAddress); err != nil {
			return err
		}
	}
	for _, e := range snap.Names {
		if _, err := tx.Exec(`
INSERT INTO names(name, owner_key, expiration_height, first_claim_height, last_update_height)
VALUES(?, ?, ?, ?, ?)`, e.Name, e.OwnerKey, e.ExpirationHeight, e.FirstClaimHeight, e.LastUpdateHeight); err != nil {
			return err
		}
		for i, txid := range e.SourceTxIDs {
			if _, err := tx.Exec(`INSERT INTO name_txids(name, txid, position) VALUES(?, ?, ?)`, e.Name, txid, i); err != nil {
				return err
			}
		}
	}
	for _, e := range snap.Events {
		if _, err := tx.Exec(`
INSERT INTO events(txid, height, amount_atomic, years, name, owner_key, action, reason,
                   previous_expiration_height, expiration_height)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.TxID, e.Height, e.Amount, e.Years, nullString(e.Name), nullString(e.OwnerKey),
			e.Action, nullString(e.Reason), nullUint(e.PreviousExpirationHeight), nullUint(e.ExpirationHeight)); err != nil {
			return fmt.Errorf("event %s: %w", e.TxID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	ok = true
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullUint(n uint64) any {
	if n == 0 {
		return nil
	}
	return n
}

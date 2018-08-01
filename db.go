package main

import (
	"database/sql"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var insertStmt, updateStmt, deleteStmt, testingStmt, insertQueueStmt, deleteQueueStmt *sql.Stmt

var mutexSQL = &sync.Mutex{}

func openDb() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", config.Db.Dbfile+"?cache=shared&mode=rwc&_loc=auto")
	if err != nil {
		return db, err
	}

	sql := `
	CREATE TABLE IF NOT EXISTS ` + config.Db.Dbprefix + `brains (
		Passphrase primary key,
		Address,
		PrivkeyWIF,         
		CompressedAddress,   
		CompressedPrivkeyWIF,
		Confirmed,
		Unconfirmed,
		LastTxTime,
		NumTx,
		ConfirmedCompressed,
		UnconfirmedCompressed,
		LastTxTimeCompressed,
		NumTxCompressed,
		Inserted DEFAULT CURRENT_TIMESTAMP
	)
	`
	_, err = db.Exec(sql)
	if err != nil {
		return db, err
	}

	sql = `
		CREATE TABLE IF NOT EXISTS ` + config.Db.Dbprefix + `queue (
			Passphrase primary key,
			testing DEFAULT 0,
			Inserted DEFAULT CURRENT_TIMESTAMP
		)
		`

	_, err = db.Exec(sql)
	if err != nil {
		return db, err
	}

	insertQueueStmt, err = db.Prepare(`
	INSERT OR IGNORE INTO ` + config.Db.Dbprefix + `queue (Passphrase) 
	values(?)
	`)
	if err != nil {
		return db, err
	}

	deleteQueueStmt, err = db.Prepare(`
	DELETE FROM ` + config.Db.Dbprefix + `queue 
	WHERE Passphrase=?
	`)
	if err != nil {
		return db, err
	}

	insertStmt, err = db.Prepare(`
	INSERT OR REPLACE INTO ` + config.Db.Dbprefix + `brains 
	(Passphrase, Address, PrivkeyWIF, CompressedAddress, CompressedPrivkeyWIF, 
	Confirmed, Unconfirmed, LastTxTime, NumTx, ConfirmedCompressed, UnconfirmedCompressed, LastTxTimeCompressed, NumTxCompressed) 
	values(?,?,?,?,?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return db, err
	}

	testingStmt, err = db.Prepare(`
	UPDATE ` + config.Db.Dbprefix + `queue
	SET testing=1
	WHERE Passphrase=?
	`)
	if err != nil {
		return db, err
	}

	return db, err
}

func insertQueueDb(value string, db *sql.DB) error {
	_, err := insertQueueStmt.Exec(value)
	if err != nil {
		return err
	}
	return nil
}

func deleteQueueDb(value string, db *sql.DB) error {
	_, err := deleteQueueStmt.Exec(value)
	if err != nil {
		return err
	}
	return nil
}

func insertDb(result BrainResult, db *sql.DB) error {
	_, err := insertStmt.Exec(result.Address.Passphrase, result.Address.Address, result.Address.PrivkeyWIF, result.Address.CompressedAddress, result.Address.CompressedPrivkeyWIF, result.Confirmed, result.Unconfirmed, result.LastTxTime, result.NumTx, result.ConfirmedCompressed, result.UnconfirmedCompressed, result.LastTxTimeCompressed, result.NumTxCompressed)
	if err != nil {
		return err
	}
	return nil
}

func testingDb(passphrase string, db *sql.DB) error {
	_, err := testingStmt.Exec(passphrase)
	if err != nil {
		return err
	}
	return nil
}

func closeDb(db *sql.DB) error {
	mutexSQL.Lock()
	_, err := db.Exec("UPDATE " + config.Db.Dbprefix + "queue SET testing=0")
	if err != nil {
		return err
	}
	db.Close()
	mutexSQL.Unlock()
	return nil
}

package main

import (
	"database/sql"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var insertStmt, updateStmt, deleteStmt, testingStmt, insertQueueStmt, deleteQueueStmt *sql.Stmt

var mutexSQL = &sync.Mutex{}

func openDb() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", config.Db.Dbfile+"?cache=shared&mode=rwc")
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
		Inserted DEFAULT CURRENT_TIMESTAMP,
		testing DEFAULT 0,
		Checked 	
	)
	`
	_, err = db.Exec(sql)
	if err != nil {
		return db, err
	}

	sql = `
		CREATE TABLE IF NOT EXISTS ` + config.Db.Dbprefix + `queue (
			Passphrase primary key,
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
	INSERT OR IGNORE INTO ` + config.Db.Dbprefix + `brains 
	(Passphrase, Address, PrivkeyWIF, CompressedAddress, CompressedPrivkeyWIF) 
	values(?,?,?,?,?)
	`)
	if err != nil {
		return db, err
	}

	updateStmt, err = db.Prepare(`
	UPDATE ` + config.Db.Dbprefix + `brains 
	SET Confirmed=?, Unconfirmed=?, LastTxTime=?, NumTx=?, 
	ConfirmedCompressed=?, UnconfirmedCompressed=?, LastTxTimeCompressed=?, NumTxCompressed=?,
	testing=0, Checked=datetime('now')
	WHERE Passphrase=?
	`)
	if err != nil {
		return db, err
	}

	deleteStmt, err = db.Prepare(`
	DELETE FROM ` + config.Db.Dbprefix + `brains 
	WHERE Passphrase=?
	`)
	if err != nil {
		return db, err
	}

	testingStmt, err = db.Prepare(`
	UPDATE ` + config.Db.Dbprefix + `brains
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

func insertDb(address BrainAddress, db *sql.DB) error {
	_, err := insertStmt.Exec(address.Passphrase, address.Address, address.PrivkeyWIF, address.CompressedAddress, address.CompressedPrivkeyWIF)
	if err != nil {
		return err
	}
	return nil
}

func updateDb(res BrainResult, db *sql.DB) error {
	var err error
	if res.NumTx+res.NumTxCompressed == 0 {
		_, err = deleteStmt.Exec(res.Address.Passphrase)
	} else {
		_, err = updateStmt.Exec(res.Confirmed, res.Unconfirmed, res.LastTxTime, res.NumTx, res.ConfirmedCompressed, res.UnconfirmedCompressed, res.LastTxTimeCompressed, res.NumTxCompressed, res.Address.Passphrase)
	}
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
	_, err := db.Exec("UPDATE " + config.Db.Dbprefix + "brains SET testing=0")
	if err != nil {
		return err
	}
	db.Close()
	mutexSQL.Unlock()
	return nil
}

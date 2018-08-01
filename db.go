package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"

	_ "github.com/mattn/go-sqlite3"
)

var insertStmt *sql.Stmt

func opennExportDb() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", config.Db.Exportdbfile+"?cache=shared&mode=rwc&_loc=auto")
	if err != nil {
		return db, err
	}

	sql := `
	CREATE TABLE IF NOT EXISTS ` + config.Db.Exportdbprefix + `brains (
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

	insertStmt, err = db.Prepare(`
	INSERT OR REPLACE INTO ` + config.Db.Exportdbprefix + `brains 
	(Passphrase, Address, PrivkeyWIF, CompressedAddress, CompressedPrivkeyWIF, 
	Confirmed, Unconfirmed, LastTxTime, NumTx, ConfirmedCompressed, UnconfirmedCompressed, LastTxTimeCompressed, NumTxCompressed) 
	values(?,?,?,?,?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return db, err
	}

	return db, err
}

/*
func insertExportDb(result BrainResult, db *sql.DB) error {
	_, err := insertStmt.Exec(result.Address.Passphrase, result.Address.Address, result.Address.PrivkeyWIF, result.Address.CompressedAddress, result.Address.CompressedPrivkeyWIF, result.Confirmed, result.Unconfirmed, result.LastTxTime, result.NumTx, result.ConfirmedCompressed, result.UnconfirmedCompressed, result.LastTxTimeCompressed, result.NumTxCompressed)
	if err != nil {
		return err
	}
	return nil
}
*/

func closeDb(db *leveldb.DB) {
	fixQueue(db)
	db.Close()
}

func fixQueue(db *leveldb.DB) {
	// reset queue rows from testing -> totest status
	iter := db.NewIterator(util.BytesPrefix([]byte("testing|")), nil)
	for iter.Next() {
		key := iter.Key()
		pass := strings.TrimLeft(string(key), "testing|")
		err := db.Put([]byte("totest|"+pass), []byte("1"), nil)
		if err != nil {
			log.Println("error setting testing -> totest a queue item: ", err.Error())
			continue
		}
		err = db.Delete(iter.Key(), nil)
		if err != nil {
			log.Println("error removing testing queue a queue item: " + err.Error())
		}
	}
	iter.Release()
	err := iter.Error()
	if err != nil {
		fmt.Println("error shutting down working db: ", err.Error())
	}
}

func closeExportDb(db *sql.DB) {
	exportdb.Close()
}

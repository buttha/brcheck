/*
db entries are ( key = value ):

totest|word = 1 : word in the queue waiting to be converted
converted|word = {json brainwallet} : item converted and waiting to be tested (totest|word is removed from db)
testing|word = {json brainwallet} : word converted and under electrum's test  (converted|word is removed from db)
result|word = {json result} : a positive result is stored as json (testing|word is removed from db)

when program starts and stops, testing|word are resetted into converted|word status, since we don't have a result yet
*/

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"

	_ "github.com/mattn/go-sqlite3"
)

var insertStmt *sql.Stmt

var mutexSQL = &sync.Mutex{}

func opennExportDb() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", config.Db.Exportdbfile+"?cache=shared&mode=rwc&_loc=auto")
	if err != nil {
		return db, err
	}

	sql := `
	CREATE TABLE IF NOT EXISTS ` + config.Db.Exportdbtable + ` (
		Passphrase primary key,
		Confirmed,
		Unconfirmed,
		ConfirmedCompressed,
		UnconfirmedCompressed,
		LastTxTime,
		LastTxTimeCompressed,
		Address,
		PrivkeyWIF,         
		CompressedAddress,   
		CompressedPrivkeyWIF,
		NumTx,
		NumTxCompressed,
		Inserted DEFAULT CURRENT_TIMESTAMP
	)
	`
	_, err = db.Exec(sql)
	if err != nil {
		return db, err
	}

	insertStmt, err = db.Prepare(`
	INSERT OR REPLACE INTO ` + config.Db.Exportdbtable + `
	(Passphrase, Address, PrivkeyWIF, CompressedAddress, CompressedPrivkeyWIF, 
	Confirmed, Unconfirmed, LastTxTime, NumTx, ConfirmedCompressed, UnconfirmedCompressed, LastTxTimeCompressed, NumTxCompressed) 
	values(?,?,?,?,?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return db, err
	}

	return db, err
}

func exportdbcron(db *leveldb.DB, exportdb *sql.DB) {
	// manage db export every exportinterval seconds
	exportdbChan := time.NewTicker(time.Second * time.Duration(config.Db.Exportdbinterval)).C
	go func() {
		for {
			<-exportdbChan
			doexportdb(db, exportdb)
		}
	}()

}

func doexportdb(db *leveldb.DB, exportdb *sql.DB) {
	var brain BrainResult
	for {
		mutexSQL.Lock()
		tx, err := exportdb.Begin() // init transaction
		if err != nil {
			log.Println("error starting exportdb transaction:", err.Error())
			mutexSQL.Unlock()
			break
		}

		iter := db.NewIterator(util.BytesPrefix([]byte("result|")), nil)
		for iter.Next() {
			err = json.Unmarshal(iter.Value(), &brain)
			if err != nil {
				log.Println("exportdb: error decoding a result row:", err.Error())
				continue
			}
			_, err = insertStmt.Exec(brain.Address.Passphrase, brain.Address.Address, brain.Address.PrivkeyWIF, brain.Address.CompressedAddress, brain.Address.CompressedPrivkeyWIF, brain.Confirmed, brain.Unconfirmed, brain.LastTxTime, brain.NumTx, brain.ConfirmedCompressed, brain.UnconfirmedCompressed, brain.LastTxTimeCompressed, brain.NumTxCompressed)
			if err != nil {
				log.Println("exportdb: error writing a result row:", err.Error())
				continue
			}
		}
		iter.Release()
		tx.Commit()
		mutexSQL.Unlock()
		break
	}
}

func fixQueue(db *leveldb.DB) {
	// reset queue rows from testing -> converted status
	iter := db.NewIterator(util.BytesPrefix([]byte("testing|")), nil)
	for iter.Next() {
		key := iter.Key()
		pass := strings.TrimLeft(string(key), "testing|")
		err := db.Put([]byte("converted|"+pass), iter.Value(), nil)
		if err != nil {
			log.Println("error setting testing -> converted a queue item: ", err.Error())
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
		log.Println("error shutting down working db: ", err.Error())
	}
}

func dumpdb(db *leveldb.DB) {
	iter := db.NewIterator(util.BytesPrefix([]byte("converted|")), nil)
	//iter := db.NewIterator(util.BytesPrefix([]byte("result|")), nil)
	//iter := db.NewIterator(nil, nil)
	for iter.Next() {
		key := iter.Key()
		value := iter.Value()
		fmt.Println(string(key), string(value))
	}
	iter.Release()
	return
}

func closeDb(db *leveldb.DB) {
	fixQueue(db)
	db.Close()
}

func closeExportDb(exportdb *sql.DB) {
	exportdb.Close()
}

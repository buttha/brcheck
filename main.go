package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"time"
)

// BrainResult for tests' result
type BrainResult struct {
	Address               BrainAddress
	Confirmed             uint64 // balance
	Unconfirmed           uint64 // balance
	LastTxTime            time.Time
	NumTx                 int
	ConfirmedCompressed   uint64
	UnconfirmedCompressed uint64
	LastTxTimeCompressed  time.Time
	NumTxCompressed       int
}

var config Config
var db *sql.DB
var stattotaltests, statminutetests, statfound, statbrainsgenerated uint64 // for stats
var activetests uint64

func main() {

	wordschan := make(chan BrainAddress)     // chan for string to test
	nottestedchan := make(chan BrainAddress) // if a goroutine failed to test a string (ex. server disconnected, ecc...) then string will be send back through this channel
	resultschan := make(chan BrainResult)    // tests' results

	cfg, err := ParseConfig() // reads command line params and config file
	if err != nil {
		fmt.Println("Error reading command line params:", err)
		return
	}
	config = cfg
	if config.Db.Dbfile == "" {
		fmt.Println("Missing database file name. Use config file or command line parameter -dbfile")
		return
	}

	db, err = openDb()
	if err != nil {
		fmt.Println("Error opening db:", err)
		return
	}
	defer closeDb(db)

	manageshutdown() // detect program interruption

	stats() // manage statistics

	go Establishconnections(wordschan, nottestedchan, resultschan) // establish electrum's connections

	finishedqueue := make(chan bool)   // finished reading queue
	finishedtesting := make(chan bool) // finished testing passphrases

	go goresults(wordschan, nottestedchan, resultschan)   // manage electrum's results
	go goqueue(finishedqueue)                             // manage queue
	go gotests(finishedqueue, finishedtesting, wordschan) // test db data

	// main cicle
	scanner := bufio.NewScanner(os.Stdin) // read from standard input

	// check if thereis something to read from stdin
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		mutexSQL.Lock()
		tx, err := db.Begin() // init transaction
		checkErr("feeding queue from stdin ", err)
		for scanner.Scan() {
			err = insertQueueDb(scanner.Text(), db)
			if err != nil {
				log.Println("error inserting in db queue a row to test: " + err.Error())
				tx.Rollback() // rollback transaction
				break
			}
		}
		tx.Commit() // commit transaaction
		mutexSQL.Unlock()
	}
	<-finishedtesting // wait the end of tests

}

func checkErr(where string, err error) {
	if err != nil {
		log.Printf("error " + where + ":")
		panic(err)
	}
}

func manageshutdown() { // detect program interrupt
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		log.Println("Received an interrupt, stopping service...")
		closeDb(db)
		log.Println("...done")
		os.Exit(0)
	}()
}

func stats() {
	if config.Log.Nostats == false {
		statsChan := time.NewTicker(time.Second * 60).C
		go func() {
			for {
				<-statsChan
				avgmin := float64(atomic.LoadUint64(&statminutetests)) / 60.0
				brainsgeneratedpersec := float64(atomic.LoadUint64(&statbrainsgenerated)) / 60.0
				log.Printf("[STATS] Total tests: %d | Last minute: %d | Last minute average: %.2f tests/s | Addresses found: %d | Brains generated: %d (%.2f/s)\n", atomic.LoadUint64(&stattotaltests), atomic.LoadUint64(&statminutetests), avgmin, atomic.LoadUint64(&statfound), atomic.LoadUint64(&statbrainsgenerated), brainsgeneratedpersec)
				atomic.StoreUint64(&statminutetests, 0)
				atomic.StoreUint64(&statbrainsgenerated, 0)
			}
		}()
	}
}

// goresults: manage electrum's results
func goresults(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {
	var nottested BrainAddress
	var result BrainResult
	for {
		select {
		case nottested = <-nottestedchan:
			wordschan <- nottested // resubmit to another server
		case result = <-resultschan: // here it's the result
			mutexSQL.Lock()
			err := updateDb(result, db)
			mutexSQL.Unlock()
			atomic.AddUint64(&activetests, ^uint64(0)) // activetests--
			if err != nil {
				log.Println("error writing a result in db: " + err.Error())
			}
			if result.NumTx+result.NumTxCompressed != 0 {
				if config.Log.Logresult {
					log.Printf("%+v\n", result)
				}
				atomic.AddUint64(&statfound, 1)
			}
			atomic.AddUint64(&stattotaltests, 1) // stats
			atomic.AddUint64(&statminutetests, 1)
		}
	}
}

// goqueue: manage queue
func goqueue(finishedqueue chan bool) {
	var address BrainAddress
	for {
		numqrows := 0
		mutexSQL.Lock()
		limit := len(connectedpeers)
		if limit < 10 {
			limit = 10
		}
		limit = 1
		rows, err := db.Query("SELECT Passphrase FROM " + config.Db.Dbprefix + "queue ORDER BY Inserted LIMIT " + strconv.Itoa(limit))
		checkErr("during SELECT queue rows to elaborate", err)
		var insertqueue []BrainAddress
		var tpass string
		var tpasses []string
		for rows.Next() { // read
			err = rows.Scan(&tpass)
			checkErr("reading queue row to process", err)
			tpasses = append(tpasses, tpass)
			numqrows++
		}
		rows.Close()
		mutexSQL.Unlock()
		for _, tpass := range tpasses { // convert
			address = BrainGenerator(tpass)
			insertqueue = append(insertqueue, address)
			atomic.AddUint64(&statbrainsgenerated, 1)
		}
		mutexSQL.Lock()
		tx, err := db.Begin()
		for _, taddr := range insertqueue { // write
			err = insertDb(taddr, db)
			checkErr("inserting in db a row to test:", err)
			err = deleteQueueDb(taddr.Passphrase, db)
			checkErr("removing a queue line", err)
		}
		tx.Commit()
		mutexSQL.Unlock()

		if numqrows == 0 {
			finishedqueue <- true
		}
	}
}

// gotests: test db data
func gotests(finishedqueue, finishedtesting chan bool, wordschan chan BrainAddress) {
	queueempty := false
	for {
		select {
		case <-finishedqueue:
			queueempty = true
		default:
		}

		// elaborate brains
		numrows := 0
		var undertest []BrainAddress
		mutexSQL.Lock()
		rows, err := db.Query("SELECT Passphrase, Address, PrivkeyWIF, CompressedAddress, CompressedPrivkeyWIF FROM " + config.Db.Dbprefix + "brains WHERE testing=0 AND Checked IS NULL ORDER BY Inserted")
		checkErr("during SELECT rows to test", err)
		var (
			tPassphrase           string
			tAddress              string
			tPrivkeyWIF           string
			tCompressedAddress    string
			tCompressedPrivkeyWIF string
		)

		for rows.Next() {
			err = rows.Scan(&tPassphrase, &tAddress, &tPrivkeyWIF, &tCompressedAddress, &tCompressedPrivkeyWIF)
			checkErr("reading row to test", err)
			numrows++
			undertest = append(undertest, BrainAddress{
				Passphrase:           tPassphrase,
				Address:              tAddress,
				PrivkeyWIF:           tPrivkeyWIF,
				CompressedAddress:    tCompressedAddress,
				CompressedPrivkeyWIF: tCompressedPrivkeyWIF,
			})
		}
		rows.Close()
		mutexSQL.Unlock()
		for _, totest := range undertest { // mark line as under testing
			wordschan <- totest
			atomic.AddUint64(&activetests, 1) // activetests++
			mutexSQL.Lock()
			err = testingDb(totest.Passphrase, db)
			checkErr("setting test flag", err)
			mutexSQL.Unlock()
		}

		if numrows == 0 && queueempty && atomic.LoadUint64(&activetests) == 0 {
			finishedtesting <- true
			return
		}

	}
}

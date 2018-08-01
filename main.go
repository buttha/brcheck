package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"time"
	// _ "github.com/pkg/profile" // profiling: https://flaviocopes.com/golang-profiling/
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
	//defer profile.Start(profile.MemProfile).Stop() // memory
	//defer profile.Start().Stop() // cpu
	/*
		go tool pprof --pdf ~/go/bin/yourbinary /var/path/to/file.pprof > file.pdf
		see https://flaviocopes.com/golang-profiling/
	*/

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
	go Keepconnections(wordschan, nottestedchan, resultschan)      // peers discovery

	finishedqueue := make(chan bool)   // finished reading queue
	finishedtesting := make(chan bool) // finished testing passphrases

	go goresults(wordschan, nottestedchan, resultschan, finishedtesting, finishedqueue) // manage electrum's results
	go goqueue(wordschan, finishedqueue)                                                // manage queue

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
func goresults(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult, finishedtesting, finishedqueue chan bool) {
	var nottested BrainAddress
	var result BrainResult
	for {

		select {
		case <-finishedqueue:
			if atomic.LoadUint64(&activetests) == 0 {
				finishedtesting <- true
			}
		default:
		}

		select {
		case nottested = <-nottestedchan:
			wordschan <- nottested // resubmit to another server
		case result = <-resultschan: // here it's the result
			atomic.AddUint64(&activetests, ^uint64(0)) // activetests--
			mutexSQL.Lock()
			if result.NumTx+result.NumTxCompressed != 0 {
				if config.Log.Logresult {
					log.Printf("%+v\n", result)
				}
				atomic.AddUint64(&statfound, 1)

				err := insertDb(result, db)
				if err != nil {
					log.Println("error writing a result in db: " + err.Error())
				}
			}
			err := deleteQueueDb(result.Address.Passphrase, db)
			checkErr("removing a queue line", err)
			mutexSQL.Unlock()
			atomic.AddUint64(&stattotaltests, 1) // stats
			atomic.AddUint64(&statminutetests, 1)
		default:
		}
	}
}

// goqueue: manage queue
func goqueue(wordschan chan BrainAddress, finishedqueue chan bool) {
	var address BrainAddress
	var pass string
	for {
		mutexSQL.Lock()
		err := db.QueryRow("SELECT Passphrase FROM " + config.Db.Dbprefix + "queue WHERE testing=0 ORDER BY Inserted LIMIT 1").Scan(&pass)
		mutexSQL.Unlock()

		switch {
		case err == sql.ErrNoRows:
			finishedqueue <- true
			time.Sleep(100 * time.Millisecond)
			continue
		case err != nil:
			checkErr("during SELECT queue row to elaborate", err)
		}

		address = BrainGenerator(pass)
		atomic.AddUint64(&statbrainsgenerated, 1)
		wordschan <- address
		atomic.AddUint64(&activetests, 1) // activetests++
		mutexSQL.Lock()
		err = testingDb(address.Passphrase, db)
		checkErr("setting test flag", err)
		mutexSQL.Unlock()

	}
}

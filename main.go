package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	// "github.com/pkg/profile" // profiling: https://flaviocopes.com/golang-profiling/
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
var stattotaltests, statminutetests, statfound, statbrainsgenerated uint64 // for stats
var activetests uint64

func main() {
	//defer profile.Start(profile.MemProfile).Stop() // memory
	//defer profile.Start().Stop() // cpu
	/*
		go tool pprof --pdf ~/go/bin/yourbinary /var/path/to/file.pprof > file.pdf
		see https://flaviocopes.com/golang-profiling/
	*/

	wordschan := make(chan BrainAddress, 100) // chan for string to test
	nottestedchan := make(chan BrainAddress)  // if a goroutine failed to test a string (ex. server disconnected, ecc...) then string will be send back through this channel
	resultschan := make(chan BrainResult)     // tests' results

	cfg, err := ParseConfig() // reads command line params and config file
	if err != nil {
		fmt.Println("Error reading command line params:", err)
		return
	}
	config = cfg

	if config.Db.Dbdir == "" {
		fmt.Println("Missing database directory. Use config file or command line parameter -dbdir")
		return
	}

	db, err := leveldb.OpenFile(config.Db.Dbdir, nil)
	if err != nil {
		fmt.Println("Error opening working database:", err)
		return
	}
	defer closeDb(db)

	fixQueue(db) // reset queue rows from testing -> totest status

	var exportdb *sql.DB
	if config.Db.Exportdbfile != "" {
		if config.Db.Exportdbtable == "" {
			fmt.Println("Missing export db tablename. Use config file or command line parameter -exportdbtable", err)
			return
		}
		exportdb, err = opennExportDb()
		if err != nil {
			fmt.Println("Error opening export db:", err)
			return
		}
		defer closeExportDb(exportdb)
		if config.Db.Exportdbinterval > 0 {
			exportdbcron(db, exportdb) // manage db export every exportdbinterval seconds
		}
		defer doexportdb(db, exportdb) // export db when finished

	}

	/*
		dumpdb(db)
		return
	*/

	manageshutdown(db, exportdb) // detect program interruption

	stats() // manage statistics

	go Establishconnections(wordschan, nottestedchan, resultschan) // establish electrum's connections
	go Keepconnections(wordschan, nottestedchan, resultschan)      // peers discovery

	finishedqueue := make(chan bool)   // finished reading queue
	finishedtesting := make(chan bool) // finished testing passphrases
	finishedstdin := make(chan bool)   // finished reading stdin

	go goresults(wordschan, nottestedchan, resultschan, finishedtesting, finishedqueue, db) // manage electrum's results
	go goqueue(wordschan, finishedqueue, finishedstdin, db)                                 // manage queue

	// main cicle
	scanner := bufio.NewScanner(os.Stdin) // read from standard input

	// check if thereis something to read from stdin
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		log.Println("Start reading stdin")
		for scanner.Scan() {
			err = db.Put([]byte("totest|"+scanner.Text()), []byte("1"), nil)
			if err != nil {
				fmt.Println("error writing stdin to db:", err.Error())
				return
			}
		}
		log.Println("Finished reading stdin")
	}
	finishedstdin <- true // tell to goqueue we have finished reading stdin
	<-finishedtesting     // wait the end of tests
}

func manageshutdown(db *leveldb.DB, exportdb *sql.DB) { // detect program interrupt
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		log.Println("Received an interrupt, stopping service...")
		doexportdb(db, exportdb)
		closeDb(db)
		closeExportDb(exportdb)
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
func goresults(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult, finishedtesting, finishedqueue chan bool, db *leveldb.DB) {
	var nottested BrainAddress
	var result BrainResult
	for {

		if atomic.LoadUint64(&activetests) == 0 {
			select {
			case <-finishedqueue:
				finishedtesting <- true
				time.Sleep(100 * time.Millisecond)
			default:
			}
		}

		select {
		case nottested = <-nottestedchan:
			wordschan <- nottested // resubmit to another server
		case result = <-resultschan: // here it's the result
			atomic.AddUint64(&activetests, ^uint64(0)) // activetests--
			if result.NumTx+result.NumTxCompressed != 0 {
				if config.Log.Logresult {
					log.Printf("%+v\n", result)
				}
				atomic.AddUint64(&statfound, 1)
				resjson, _ := json.Marshal(result)
				err := db.Put([]byte("result|"+result.Address.Passphrase), resjson, nil)
				if err != nil {
					log.Println("error writing a result in db: " + err.Error())
				}
			}
			err := db.Delete([]byte("testing|"+result.Address.Passphrase), nil)
			if err != nil {
				log.Println("error removing a queue item from testing status: " + err.Error())
			}
			atomic.AddUint64(&stattotaltests, 1) // stats
			atomic.AddUint64(&statminutetests, 1)
		default:
		}
	}
}

// goqueue: manage queue
func goqueue(wordschan chan BrainAddress, finishedqueue, finishedstdin chan bool, db *leveldb.DB) {
	var address BrainAddress
	var pass string
	var numrows uint64
	for {
		numrows = 0
		iter := db.NewIterator(util.BytesPrefix([]byte("totest|")), nil)
		for iter.Next() {
			numrows++
			key := iter.Key()
			iter.Release()
			pass = strings.TrimLeft(string(key), "totest|")
			address = BrainGenerator(pass)
			atomic.AddUint64(&statbrainsgenerated, 1)
			wordschan <- address
			atomic.AddUint64(&activetests, 1) // activetests++

			err := db.Put([]byte("testing|"+pass), []byte("1"), nil)
			if err != nil {
				log.Println("error setting totest -> testing a queue item: ", err.Error())
				continue
			}
			err = db.Delete(key, nil)
			if err != nil {
				log.Println("error removing a testing queue row: ", err.Error())
				continue
			}
		}

		if numrows == 0 {
			select {
			case <-finishedstdin:
				iter.Release()
				finishedqueue <- true
				time.Sleep(100 * time.Millisecond)
			default:
			}
		}
	}
}

func dumpdb(db *leveldb.DB) {
	iter := db.NewIterator(util.BytesPrefix([]byte("result|")), nil)
	for iter.Next() {
		// Remember that the contents of the returned slice should not be modified, and
		// only valid until the next call to Next.
		key := iter.Key()
		value := iter.Value()
		fmt.Println(string(key), string(value))
	}
	iter.Release()
	return
}

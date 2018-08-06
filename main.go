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
	"sync"
	"sync/atomic"
	"time"

	// "github.com/pkg/profile" // profiling: https://flaviocopes.com/golang-profiling/
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
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
var mutexactivetests = &sync.Mutex{}
var exportingdb bool
var resetconn uint64 // counter for resetconn

func main() {
	//defer profile.Start(profile.MemProfile).Stop() // memory
	//defer profile.Start().Stop() // cpu
	/*
		go tool pprof --pdf ~/go/bin/yourbinary /var/path/to/file.pprof > file.pdf
		go tool pprof --text ~/go/bin/yourbinary /var/path/to/file.pprof > file.txt
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
		fmt.Println("Error opening working database "+config.Db.Dbdir+":", err)
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
		exportingdb = true
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

	resetconn = uint64(config.Conn.Resetconn)                      // restart all connections after resetconn requests
	go Establishconnections(wordschan, nottestedchan, resultschan) // establish electrum's connections
	go Keepconnections(wordschan, nottestedchan, resultschan)      // peers discovery

	finishedqueue := make(chan bool)   // finished reading queue
	finishedtesting := make(chan bool) // finished testing passphrases
	finishedstdin := make(chan bool)   // finished reading stdin
	finishedbrains := make(chan bool)  // finished converting brains

	go gobrains(finishedstdin, finishedbrains, db)                                          // convert queue rows into brainwallets
	go goqueue(wordschan, finishedqueue, finishedstdin, finishedbrains, db)                 // submit brainwallets to electrum servers
	go goresults(wordschan, nottestedchan, resultschan, finishedtesting, finishedqueue, db) // manage electrum's results

	// main cicle
	scanner := bufio.NewScanner(os.Stdin) // read from standard input

	// check if thereis something to read from stdin
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		lines := 0
		log.Println("Start reading stdin")
		for scanner.Scan() {
			err = db.Put([]byte("totest|"+scanner.Text()), []byte("1"), nil)
			if err != nil {
				fmt.Println("error writing stdin to db:", err.Error())
				return
			}
			lines++
			if lines == 100000 { // "unlock" db and let other goroutines work
				time.Sleep(time.Second)
				lines = 0
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
		if exportingdb {
			doexportdb(db, exportdb)
			closeExportDb(exportdb)
		}
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

// gobrains: convert queue rows into brainwallets
func gobrains(finishedstdin, finishedbrains chan bool, db *leveldb.DB) {
	var address BrainAddress
	var pass string
	var numrows uint64
	var lastloop bool // to do another loop when stdin is finished, to be sure everything is checked
	for {
		numrows = 0
		iter := db.NewIterator(util.BytesPrefix([]byte("totest|")), nil)
		for iter.Next() {
			numrows++
			key := iter.Key()
			pass = strings.TrimLeft(string(key), "totest|")
			address = BrainGenerator(pass)
			atomic.AddUint64(&statbrainsgenerated, 1)
			addressB, err := json.Marshal(address)
			if err != nil {
				log.Println("error encoding a brainwallet to test")
				continue
			}
			err = db.Put([]byte("converted|"+pass), addressB, nil)
			if err != nil {
				log.Println("error setting totest -> testing a queue item: ", err.Error())
				continue
			}
			err = db.Delete(key, nil)
			if err != nil {
				log.Println("error removing a testing queue row: ", err.Error())
				continue
			}
			if numrows == 1000 {
				break // otherwise goqueue doesn't have data until loop ends
			}
		}
		iter.Release()
		if numrows == 0 {
			if lastloop {
				finishedbrains <- true
				return
			} else {
				select {
				case <-finishedstdin:
					lastloop = true
				default:
				}
			}
		}
	}
}

// goqueue submit brainwallets to electrum servers
func goqueue(wordschan chan BrainAddress, finishedqueue, finishedstdin, finishedbrains chan bool, db *leveldb.DB) {
	var address BrainAddress
	var numrows uint64
	var lastloop bool // to do another loop when brains are finished, to be sure everything is checked
	for {
		numrows = 0
		iter := db.NewIterator(util.BytesPrefix([]byte("converted|")), nil)
		for iter.Next() {
			numrows++
			err := json.Unmarshal(iter.Value(), &address)
			if err != nil {
				log.Println("error decoding a brainwallet to test")
				continue
			}
			err = db.Put([]byte("testing|"+address.Passphrase), iter.Value(), nil)
			if err != nil {
				log.Println("error setting converted -> testing a queue item: ", err.Error())
				continue
			}
			err = db.Delete(iter.Key(), nil)
			if err != nil {
				log.Println("error removing a converted queue row: ", err.Error())
				continue
			}
			mutexactivetests.Lock()
			atomic.AddUint64(&activetests, 1) // activetests++
			mutexactivetests.Unlock()
			wordschan <- address
			if numrows == 1000 {
				break // release db and memory
			}
		}
		iter.Release()
		if numrows == 0 {
			if lastloop {
				finishedqueue <- true
				return
			} else {
				select {
				case <-finishedbrains:
					lastloop = true
				default:
				}
			}
		}
	}
}

// goresults: manage electrum's results
func goresults(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult, finishedtesting, finishedqueue chan bool, db *leveldb.DB) {
	var nottested BrainAddress
	var result BrainResult
	for {

		mutexactivetests.Lock()
		if atomic.LoadUint64(&activetests) == 0 {
			select {
			case <-finishedqueue:
				mutexactivetests.Unlock()
				finishedtesting <- true
				return
			default:
			}
		}
		mutexactivetests.Unlock()

		select {
		case nottested = <-nottestedchan:
			wordschan <- nottested // resubmit to another server
		case result = <-resultschan: // here it's the result
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
			mutexactivetests.Lock()
			atomic.AddUint64(&activetests, ^uint64(0)) // activetests--
			mutexactivetests.Unlock()

			atomic.AddUint64(&stattotaltests, 1) // stats
			atomic.AddUint64(&statminutetests, 1)

			// resetconn
			if config.Conn.Resetconn > 0 {
				atomic.AddUint64(&resetconn, ^uint64(0))
				if atomic.LoadUint64(&resetconn) == 0 {
					atomic.StoreUint64(&resetconn, uint64(config.Conn.Resetconn))
					Resetconnections()
					go Establishconnections(wordschan, nottestedchan, resultschan)
				}
			}
		default:
		}
	}
}

func dumpdb(db *leveldb.DB) {
	//iter := db.NewIterator(util.BytesPrefix([]byte("result|")), nil)
	iter := db.NewIterator(nil, nil)
	for iter.Next() {
		key := iter.Key()
		value := iter.Value()
		fmt.Println(string(key), string(value))
	}
	iter.Release()
	return
}

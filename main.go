package main

import (
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
var statsdbtotest, statsdbconverted, statsdbtesting, statsdbresult uint64  // for db stats
var activetests uint64
var mutexactivetests = &sync.Mutex{}
var exportingdb bool

func main() {
	// defer profile.Start(profile.MemProfile).Stop() // memory
	// defer profile.Start().Stop() // cpu
	/*
		import "github.com/pkg/profile"

		go tool pprof --pdf ~/go/bin/yourbinary /var/path/to/file.pprof > file.pdf
		go tool pprof --text ~/go/bin/yourbinary /var/path/to/file.pprof > file.txt
		see https://flaviocopes.com/golang-profiling/
	*/

	wordschan := make(chan BrainAddress, 100)     // chan for string to test
	nottestedchan := make(chan BrainAddress, 100) // if a goroutine failed to test a string (ex. server disconnected, ecc...) then string will be send back through this channel
	resultschan := make(chan BrainResult, 100)    // tests' results

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

	statsdb(db) // initialize variables about db stats

	shutdowngobrains := make(chan bool)  // used to stop gobrains (by manageshutdown)
	shutdowngoqueue := make(chan bool)   // used to stop goqueue (by manageshutdown)
	shutdowngoresults := make(chan bool) // used to stop goresults (by manageshutdown)

	manageshutdown(db, exportdb, shutdowngobrains, shutdowngoqueue, shutdowngoresults) // detect program interruption

	stats(db) // manage statistics

	go Establishconnections(wordschan, nottestedchan, resultschan) // establish electrum's connections
	go Keepconnections(wordschan, nottestedchan, resultschan)      // peers discovery

	finishedqueue := make(chan bool)   // finished reading queue
	finishedtesting := make(chan bool) // finished testing passphrases
	finishedstdin := make(chan bool)   // finished reading stdin
	finishedbrains := make(chan bool)  // finished converting brains

	go gobrains(finishedstdin, finishedbrains, shutdowngobrains, db)                                           // convert queue rows into brainwallets
	go goqueue(wordschan, finishedqueue, finishedstdin, finishedbrains, shutdowngoqueue, db)                   // submit brainwallets to electrum servers
	go goresults(wordschan, nottestedchan, resultschan, finishedtesting, finishedqueue, shutdowngoresults, db) // manage electrum's results

	// main cicle

	if config.Crawler.Starturl != "" { // web crawler
		crawler(db)
	} else { // stdin
		stdin(db)
	}

	finishedstdin <- true // tell to goqueue we have finished reading stdin
	<-finishedtesting     // wait the end of tests

}

func manageshutdown(db *leveldb.DB, exportdb *sql.DB, shutdowngobrains, shutdowngoqueue, shutdowngoresults chan bool) { // detect program interrupt
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		log.Println("Received an interrupt, stopping service...")
		shutdown(shutdowngobrains, shutdowngoqueue, shutdowngoresults)
		os.Exit(0)
	}()
}

func shutdown(shutdowngobrains, shutdowngoqueue, shutdowngoresults chan bool) {
	log.Println("...stopping brainwallet's generations...")
	select {
	case shutdowngobrains <- true:
	case <-time.After(10 * time.Second):
		log.Println("...time out: forced close...")
	}
	log.Println("...stopping queue manager...")
	select {
	case shutdowngoqueue <- true:
	case <-time.After(10 * time.Second):
		log.Println("...time out: forced close...")
	}
	log.Println("...stopping results manager...")
	select {
	case shutdowngoresults <- true:
	case <-time.After(10 * time.Second):
		log.Println("...time out: forced close...")
	}
	log.Println("...done")
}

func statsdb(db *leveldb.DB) {
	iter := db.NewIterator(nil, nil)
	for iter.Next() {
		key := string(iter.Key())
		if strings.HasPrefix(key, "totest|") {
			atomic.AddUint64(&statsdbtotest, 1)
		}
		if strings.HasPrefix(key, "converted|") {
			atomic.AddUint64(&statsdbconverted, 1)
		}
		if strings.HasPrefix(key, "testing|") {
			atomic.AddUint64(&statsdbtesting, 1)
		}
		if strings.HasPrefix(key, "result|") {
			atomic.AddUint64(&statsdbresult, 1)
		}
	}
	iter.Release()
}

func stats(db *leveldb.DB) {
	if config.Log.Logstats {

		num := 0              // used to compute session's average tests per second,
		var avgavgsec float64 // in order to have a good timetocomplete estimation

		statsChan := time.NewTicker(time.Second * 60).C
		go func() {
			for {
				<-statsChan

				num++

				avgsec := float64(atomic.LoadUint64(&statminutetests)) / 60.0
				brainsgeneratedpersec := float64(atomic.LoadUint64(&statbrainsgenerated)) / 60.0

				avgavgsec = (avgavgsec*float64(num-1) + avgsec) / float64(num)
				timetocomplete := ""
				if avgavgsec != 0 {
					timetocomplete = secondsToHuman(int(float64(atomic.LoadUint64(&statsdbtotest)+atomic.LoadUint64(&statsdbconverted)) / avgavgsec))
				} else {
					timetocomplete = "NA"
				}

				log.Printf("STATS: [Total] Tests: %d | Addresses found: %d || [Last minute] Tests: %d | Average: %.2f/s | Brains generated: %d (%.2f/s) || [DB] To be converted: %d | Converted (waiting to be tested): %d | Testing: %d | Found: %d || Time to complete: %s\n", atomic.LoadUint64(&stattotaltests), atomic.LoadUint64(&statfound), atomic.LoadUint64(&statminutetests), avgsec, atomic.LoadUint64(&statbrainsgenerated), brainsgeneratedpersec, atomic.LoadUint64(&statsdbtotest), atomic.LoadUint64(&statsdbconverted), atomic.LoadUint64(&statsdbtesting), atomic.LoadUint64(&statsdbresult), timetocomplete)

				atomic.StoreUint64(&statminutetests, 0)
				atomic.StoreUint64(&statbrainsgenerated, 0)
			}
		}()
	}
}

// gobrains: convert queue rows into brainwallets
func gobrains(finishedstdin, finishedbrains, shutdowngobrains chan bool, db *leveldb.DB) {
	var address BrainAddress
	var pass string
	var numrows uint64
	var lastloop bool // to do another loop when stdin is finished, to be sure everything is checked

	for {
		numrows = 0
		iter := db.NewIterator(util.BytesPrefix([]byte("totest|")), nil)
		for iter.Next() {

			select {
			case <-shutdowngobrains:
				return
			default:
			}

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
			atomic.AddUint64(&statsdbconverted, 1)
			err = db.Delete(key, nil)
			if err != nil {
				log.Println("error removing a testing queue row: ", err.Error())
				continue
			}
			atomic.AddUint64(&statsdbtotest, ^uint64(0))
			if numrows == 1000 {
				break // otherwise goqueue doesn't have data until loop ends
			}

			if config.Core.Autobrainspeed { // computes 30% more brains than tests, to be sure there're always brainwallets to be tested
				for float64(atomic.LoadUint64(&statbrainsgenerated)) > float64(atomic.LoadUint64(&statminutetests))*1.3 {
					time.Sleep(10 * time.Millisecond)
				}
			}

		}
		iter.Release()

		if numrows == 0 {
			if lastloop {
				select {
				case finishedbrains <- true:
					<-shutdowngobrains // to permit shutdown routine to end (if it's waiting for shutdown chan to be read)
					return
				case <-shutdowngobrains:
					return
				}
			} else {
				select {
				case <-finishedstdin:
					lastloop = true
				default:
					time.Sleep(2 * time.Second)
				}
			}
		}

		select {
		case <-shutdowngobrains:
			return
		default:
		}

	}
}

// goqueue submit brainwallets to electrum servers
func goqueue(wordschan chan BrainAddress, finishedqueue, finishedstdin, finishedbrains, shutdowngoqueue chan bool, db *leveldb.DB) {
	var address BrainAddress
	var numrows uint64
	var lastloop bool // to do another loop when brains are finished, to be sure everything is checked
	for {
		numrows = 0
		iter := db.NewIterator(util.BytesPrefix([]byte("converted|")), nil)
		for iter.Next() {

			select {
			case <-shutdowngoqueue:
				return
			default:
			}

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
			atomic.AddUint64(&statsdbtesting, 1)
			err = db.Delete(iter.Key(), nil)
			if err != nil {
				log.Println("error removing a converted queue row: ", err.Error())
				continue
			}
			atomic.AddUint64(&statsdbconverted, ^uint64(0))
			mutexactivetests.Lock()
			atomic.AddUint64(&activetests, 1) // activetests++
			mutexactivetests.Unlock()
			select {
			case wordschan <- address:
			case <-shutdowngoqueue:
				return
			}
			if numrows == 1000 {
				break // release db and memory
			}
		}
		iter.Release()

		if numrows == 0 {
			if lastloop {
				select {
				case finishedqueue <- true:
					<-shutdowngoqueue // to permit shutdown routine to end (if it's waiting for shutdown chan to be read)
					return
				case <-shutdowngoqueue:
					return
				}
			} else {
				select {
				case <-finishedbrains:
					lastloop = true
				default:
					time.Sleep(2 * time.Second)
				}
			}
		}

		select {
		case <-shutdowngoqueue:
			return
		default:
		}

	}
}

// goresults: manage electrum's results
func goresults(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult, finishedtesting, finishedqueue, shutdowngoresults chan bool, db *leveldb.DB) {
	var nottested BrainAddress
	var result BrainResult
	for {

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
				atomic.AddUint64(&statsdbresult, 1)
			}
			err := db.Delete([]byte("testing|"+result.Address.Passphrase), nil)
			if err != nil {
				log.Println("error removing a queue item from testing status: " + err.Error())
			}
			atomic.AddUint64(&statsdbtesting, ^uint64(0))
			mutexactivetests.Lock()
			atomic.AddUint64(&activetests, ^uint64(0)) // activetests--
			mutexactivetests.Unlock()

			atomic.AddUint64(&stattotaltests, 1) // stats
			atomic.AddUint64(&statminutetests, 1)

		case <-shutdowngoresults:
			return

		case <-time.After(time.Second):
			mutexactivetests.Lock()
			if atomic.LoadUint64(&activetests) == 0 {
				select {
				case <-finishedqueue:
					mutexactivetests.Unlock()
					finishedtesting <- true
					<-shutdowngoresults // to permit shutdown routine to end (if it's waiting for shutdown chan to be read)
					return
				default:
				}
			}
			mutexactivetests.Unlock()

		}
	}
}

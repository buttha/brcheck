package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
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

func main() {

	var address BrainAddress

	wordschan := make(chan BrainAddress)     // chan for string to test
	nottestedchan := make(chan BrainAddress) // if a goroutine failed to test a string (ex. server disconnected, ecc...) then string will be send back through this channel
	resultschan := make(chan BrainResult)    // tests' results

	var nottested BrainAddress
	var result BrainResult

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

	var db *sql.DB
	db, err = openDb()
	if err != nil {
		fmt.Println("Error opening db:", err)
		return
	}

	defer closeDb(db)
	// detect program interrupt
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		log.Println("Received an interrupt, stopping service...")
		closeDb(db)
		log.Println("...done")
		os.Exit(0)
	}()

	// stats
	var totaltests, minutetests, found uint64
	if config.Log.Nostats == false {
		start := time.Now() // for future use
		_ = start
		statsChan := time.NewTicker(time.Second * 60).C
		go func() {
			for {
				<-statsChan
				avgmin := float64(float64(minutetests) / 60)
				log.Printf("[STATS] Total tests: %d | Last minute: %d | Last minute average: %.2f tests/s | Addresses found: %d\n", totaltests, minutetests, avgmin, found)
				minutetests = 0
			}
		}()
	}

	go Establishconnections(wordschan, nottestedchan, resultschan) // establish electrum's connections

	finishedqueue := make(chan bool)   // finished reading queue
	finishedtesting := make(chan bool) // finished testing passphrases

	activetests := 0
	go func() { // manage electrum's results
		for {
			select {
			case nottested = <-nottestedchan:
				wordschan <- nottested // resubmit to another server
			case result = <-resultschan: // here it's the result
				mutexSQL.Lock()
				err = updateDb(result, db)
				mutexSQL.Unlock()
				activetests--
				if err != nil {
					log.Println("error writing a result in db: " + err.Error())
				}
				if result.NumTx+result.NumTxCompressed != 0 {
					if config.Log.Logresult {
						log.Printf("%+v\n", result)
					}
					found++
				}
				totaltests++ // stats
				minutetests++
			}
		}
	}()

	go func() { // manage queue
		for {
			numqrows := 0
			mutexSQL.Lock()
			limit := len(connectedpeers) * 2
			if limit < 10 {
				limit = 10
			}
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
			}
			for _, taddr := range insertqueue { // write
				mutexSQL.Lock()
				err = insertDb(taddr, db)
				checkErr("inserting in db a row to test:", err)
				err = deleteQueueDb(taddr.Passphrase, db)
				checkErr("removing a queue line", err)
				mutexSQL.Unlock()
			}

			if numqrows == 0 {
				finishedqueue <- true
			}
		}
	}()

	go func() { // test db data
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
				activetests++
				mutexSQL.Lock()
				err = testingDb(totest.Passphrase, db)
				checkErr("setting test flag", err)
				mutexSQL.Unlock()
			}

			if numrows == 0 && queueempty && activetests == 0 {
				finishedtesting <- true
				return
			}

		}
	}()

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

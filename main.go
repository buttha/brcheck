package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
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
		start := time.Now()
		statsChan := time.NewTicker(time.Second * 60).C
		go func() {
			for {
				<-statsChan
				avg := float64(totaltests) / time.Since(start).Seconds()
				avgmin := float64(float64(minutetests) / 60)
				log.Printf("[STATS] Total tests: %d | Average  %.2f tests/s | Last minute: %d | Last minute average: %.2f tests/s | Addresses found: %d\n", totaltests, avg, minutetests, avgmin, found)
				minutetests = 0
			}
		}()
	}

	go Establishconnections(wordschan, nottestedchan, resultschan) // establish electrum's connections

	finishedscanning := make(chan bool)         // finished reading stdin
	finishedtesting := make(chan bool)          // finished testing passphrases
	writeNewAddrToDb := make(chan BrainAddress) // write new address to db (used by main cicle to pass address to db goroutine)

	go func() { // test db data
		activetests := 0
		readinginput := true
		for {

			select {
			case <-finishedscanning:
				readinginput = false
			case nottested = <-nottestedchan:
				wordschan <- nottested // resubmit to another server
			case totest := <-writeNewAddrToDb:
				err = insertDb(totest, db)
				if err != nil {
					log.Println("error inserting in db a row to test: " + err.Error())
				}
			case result = <-resultschan: // here it's the result
				activetests--
				err = updateDb(result, db)
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
			default:
			}

			numrows := 0
			var checking []string // used to mark
			rows, err := db.Query("SELECT Passphrase, Address, PrivkeyWIF, CompressedAddress, CompressedPrivkeyWIF FROM " + config.Db.Dbprefix + "brains WHERE testing=0 AND Checked IS NULL")
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
				wordschan <- BrainAddress{
					Passphrase:           tPassphrase,
					Address:              tAddress,
					PrivkeyWIF:           tPrivkeyWIF,
					CompressedAddress:    tCompressedAddress,
					CompressedPrivkeyWIF: tCompressedPrivkeyWIF,
				}
				checking = append(checking, tPassphrase)
				activetests++
			}
			rows.Close()
			for _, undercheck := range checking { // mark line as under testing
				err = testingDb(undercheck, db)
				checkErr("setting test flag", err)

			}

			if numrows == 0 && !readinginput && activetests == 0 {
				finishedtesting <- true
				return
			}

		}
	}()

	// main cicle
	scanner := bufio.NewScanner(os.Stdin) // read from standard input
	for scanner.Scan() {
		address = BrainGenerator(scanner.Text())
		writeNewAddrToDb <- address
	}
	finishedscanning <- true // say "I've finished reading from stdin"
	<-finishedtesting        // wait the end of tests

}

func checkErr(where string, err error) {
	if err != nil {
		log.Printf("error " + where + ":")
		panic(err)
	}
}

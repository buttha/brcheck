package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
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

var paramLognet *bool   // -lognet : log network activity ( default: false )
var paramMaxconn *int   // -maxconn 10 : max electrum's connections ( default: 100 )
var paramNostats *bool  // -nostats : don't log activity stats
var paramResetconn *int // -resetconn 200 : reset connection with an electrum peer after paramResetconn requests ( default: 100 )
// this is circumvent BANDWIDTH_LIMIT see http://electrumx.readthedocs.io/en/latest/environment.html#envvar-BANDWIDTH_LIMIT

func main() {

	var address BrainAddress

	wordschan := make(chan BrainAddress)     // chan for string to test
	nottestedchan := make(chan BrainAddress) // if a goroutine failed to test a string (ex. server disconnected, ecc...) then string will be send back through this channel
	resultschan := make(chan BrainResult)    // tests' results

	var nottested BrainAddress
	var result BrainResult

	var workingtests int

	paramLognet = flag.Bool("lognet", false, "log network activity")
	paramMaxconn = flag.Int("maxconn", 100, "max electrum's connections")
	paramNostats = flag.Bool("nostats", false, "don't log activity stats")
	paramResetconn = flag.Int("resetconn", 100, " reset connection with an electrum peer after N requests")
	flag.Parse()

	// stats
	var totaltests, minutetests, found uint64
	if *paramNostats == false {
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

	scanner := bufio.NewScanner(os.Stdin) // read from standard input. Next release: 0mq
	for {

		if scanner.Scan() {
			address = BrainGenerator(scanner.Text())
			wordschan <- address
			workingtests++
		}
		select {
		case nottested = <-nottestedchan:
			wordschan <- nottested // resubmit to another server
		case result = <-resultschan: // here it's the result
			workingtests--
			if result.NumTx+result.NumTxCompressed != 0 {
				fmt.Printf("%+v\n", result)
				found++
			}
			totaltests++ // stats
			minutetests++
		default:
		}

		if workingtests == 0 {
			break
		}
	}
}

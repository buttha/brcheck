package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/BurntSushi/toml" // for configuration file
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

	var workingtests int

	cfg, err := ParseConfig() // reads command line params and config file
	if err != nil {
		fmt.Println(err)
		return
	}
	config = cfg

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

	finishedscanning := make(chan bool)
	finishedtesting := make(chan bool)

	go func() { // serve tests' results
		for {
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

				if workingtests == 0 {
					select {
					case <-finishedscanning: // I can quit
						finishedtesting <- true
						return
					default:
					}
				}
			}
		}
	}()

	scanner := bufio.NewScanner(os.Stdin) // read from standard input. Next release: 0mq
	for scanner.Scan() {
		address = BrainGenerator(scanner.Text())
		wordschan <- address
		workingtests++
	}
	finishedscanning <- true
	<-finishedtesting // wait the end of tests

}

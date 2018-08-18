package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
)

func stdin(db *leveldb.DB) {
	scanner := bufio.NewScanner(os.Stdin) // read from standard input
	// check if thereis something to read from stdin
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		lines := 0
		log.Println("Start reading stdin")
		for scanner.Scan() {
			err := db.Put([]byte("totest|"+scanner.Text()), []byte("1"), nil)
			if err != nil {
				fmt.Println("error writing stdin to db:", err.Error())
				return
			}
			atomic.AddUint64(&statsdbtotest, 1)
			lines++
			if lines == 100000 { // "unlock" db and let other goroutines work
				time.Sleep(time.Second)
				lines = 0
			}
		}
		log.Println("Finished reading stdin")
	}
}

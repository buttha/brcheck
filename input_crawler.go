package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"golang.org/x/net/html"
)

/*
db entries are ( key = value ):

visit_dep|3|link| = link : link to be visited at depth 3
visit_link|link| = 3 : same (like above)

double entries are for search convenience
*/

func crawler(db *leveldb.DB) {

	var err error

	purgeDbCrawler(db) // delete all "visit_*" entries

	if config.Log.Logcrawler {
		log.Println("[CRAWLER] start fetching urls")
	}

	done := make(chan error)

	go fetch(config.Crawler.Starturl, 0, db, done)
	err = <-done

	if err != nil {
		return
	}

	var depth uint64
	var found bool
	var fetching int64
	for depth = 1; int64(depth) <= config.Crawler.Followlinks || config.Crawler.Followlinks == -1; depth++ {

		found = false // to stop if there aren't others links to visit
		fetching = 0  // number of go routines fetching links
		iter := db.NewIterator(util.BytesPrefix([]byte("visit_dep|"+strconv.Itoa(int(depth))+"|")), nil)
		for iter.Next() {
			found = true
			fetching++
			go fetch(string(iter.Value()), depth, db, done)
			for fetching >= config.Crawler.Maxconcurrency {
				err = <-done
				fetching--
				time.Sleep(time.Second) // for slow cpu usage
			}
		}
		iter.Release()

		for fetching > 0 { // wait until all links are fetched
			err = <-done
			fetching--
		}

		if found == false { // no other links to visit
			break
		}

	}

	purgeDbCrawler(db) // delete all "visit_*" entries

	if config.Log.Logcrawler {
		log.Println("[CRAWLER] finished fetching urls")
	}

}

func fetch(link string, depth uint64, db *leveldb.DB, done chan error) {

	defer func() {
		done <- nil
	}()

	content, dlinks, err := crawl(link)
	if err != nil {
		if config.Log.Logcrawler {
			log.Println("[CRAWLER] error: " + err.Error())
		}
		done <- err
		return
	}

	for _, link := range dlinks {
		data, _ := db.Get([]byte("visit_link|"+link+"|"), nil)
		if data == nil {
			db.Put([]byte("visit_dep|"+strconv.Itoa(int(depth+1))+"|"+link+"|"), []byte(link), nil)
			db.Put([]byte("visit_link|"+link+"|"), []byte(strconv.Itoa(int(depth+1))), nil)
		}
	}

	//_ = content
	use(content, db)
}

func crawl(link string) (string, []string, error) {
	var content strings.Builder
	var laststarttoken string
	var dlinks []string

	resp, err := http.Get(link)
	if err != nil {
		return "", []string{}, err
	}

	buf, _ := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	rdr1 := ioutil.NopCloser(bytes.NewBuffer(buf)) // reader for text
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(buf)) // reader for links
	defer rdr1.Close()
	defer rdr2.Close()

	elements := html.NewTokenizer(rdr1)

	for {
		elemtype := elements.Next()

		if elemtype == html.ErrorToken {
			break
		}

		switch elemtype {
		case html.StartTagToken:
			laststarttoken = elements.Token().Data

		case html.TextToken:
			if laststarttoken == "script" {
				continue
			}
			text := strings.TrimSpace(html.UnescapeString(string(elements.Text())))
			if text != "" {
				fmt.Fprintf(&content, "%s ", text) //content = content + text + " "
			}
		}
	}

	// now links
	elements = html.NewTokenizer(rdr2)

	for {
		elemtype := elements.Next()

		if elemtype == html.ErrorToken {
			break
		}

		if elemtype == html.StartTagToken {
			t := elements.Token()
			if t.Data == "a" {
				for _, a := range t.Attr {
					if a.Key == "href" {
						// solve relatives path
						u, err := url.Parse(a.Val)
						if err != nil {
							break
						}
						base, err := url.Parse(link)
						if err != nil {
							break
						}
						dlinks = append(dlinks, base.ResolveReference(u).String())
						break
					}
				}
			}
		}
	}

	return content.String(), dlinks, nil
}

func use(text string, db *leveldb.DB) {

	words := strings.Fields(text)

	for i := 0; i <= len(words); i++ {
		for j := i + 1; j <= min(i+int(config.Crawler.Iterator), len(words)); j++ {
			str := fmt.Sprint(strings.Join(words[i:j], " "))

			err := db.Put([]byte("totest|"+str), []byte("1"), nil)
			if err != nil {
				fmt.Println("error writing crawl to db:", err.Error())
				return
			}
			atomic.AddUint64(&statsdbtotest, 1)

			if config.Crawler.Autocrawlerspeed { // computes 30% more lines than tests, to be sure there're always lines to be elaborated
				for float64(atomic.LoadUint64(&statsdbtotest)) > float64(atomic.LoadUint64(&statminutetests))*1.3 && atomic.LoadUint64(&statminutetests) != 0 {
					time.Sleep(10 * time.Millisecond)
				}
			}

		}
	}

}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

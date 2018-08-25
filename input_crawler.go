package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
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

visit_dep|3|link|0| = link : link to be visited at depth 3; *|0| = not vistied *|1| = visited
visit_link|link|0| = 3 : same (like above)

double entries are for search convenience
*/

var exitcrawler bool // used to check if shutdown signal has arrived

func crawler(shutdowncrawler chan bool, db *leveldb.DB) {

	var err error
	var depth uint64
	var found bool
	var fetching int64

	done := make(chan error)

	lastdepth := resumecrawl(db)

	if lastdepth == 0 { // not resuming: let's start from config.Crawler.Starturl
		lastdepth = 1
		if config.Log.Logcrawler {
			logger(fmt.Sprint("[CRAWLER] start fetching urls"))
		}

		go fetch(config.Crawler.Starturl, 0, db, done)
		err = <-done
		if err != nil {
			return
		}
	} else { // resume
		if config.Log.Logcrawler {
			logger(fmt.Sprint("[CRAWLER] resuming crawling"))
		}
	}

	for depth = lastdepth; int64(depth) <= config.Crawler.Followlinks || config.Crawler.Followlinks == -1; depth++ {

		found = false // to stop if there aren't others links to visit
		fetching = 0  // number of go routines fetching links
		iter := db.NewIterator(util.BytesPrefix([]byte("visit_dep|"+strconv.FormatUint(depth, 10)+"|")), nil)
		for iter.Next() {
			if strings.HasSuffix(string(iter.Key()), "|0|") { // not visited
				found = true
				fetching++
				go fetch(string(iter.Value()), depth, db, done)
				for fetching >= config.Crawler.Maxconcurrency {
					err = <-done
					fetching--
					time.Sleep(time.Second) // for slow cpu usage
				}
			}

			select {
			case <-shutdowncrawler:
				exitcrawler = true
				for fetching > 0 {
					err = <-done
					fetching--
				}
				iter.Release()
				shutdowncrawler <- true
				return
			default:
			}

		}
		iter.Release()

		select {
		case <-shutdowncrawler:
			exitcrawler = true
			for fetching > 0 {
				err = <-done
				fetching--
			}
			shutdowncrawler <- true
			return
		default:
		}

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
		logger(fmt.Sprint("[CRAWLER] finished fetching urls"))
	}

}

func resumecrawl(db *leveldb.DB) uint64 { // search the minium depth not visited yet
	var mindepth uint64
	iter := db.NewIterator(util.BytesPrefix([]byte("visit_link|")), nil)
	for iter.Next() {
		if strings.HasSuffix(string(iter.Key()), "|0|") {
			val, _ := strconv.ParseUint(string(iter.Value()), 10, 64)
			if mindepth == 0 || val < mindepth {
				mindepth = uint64(val)
			}
		}
		if mindepth == 1 {
			break
		}
	}
	iter.Release()
	return mindepth
}

func fetch(link string, depth uint64, db *leveldb.DB, done chan error) {

	defer func() {
		done <- nil
	}()

	content, dlinks, err := crawl(link)
	if err != nil {
		if config.Log.Logcrawler {
			logger(fmt.Sprint("[CRAWLER] error: " + err.Error()))
		}
		done <- err
		return
	}

	if exitcrawler { // shudown
		return
	}

	for _, url := range dlinks {
		data, _ := db.Get([]byte("visit_link|"+url+"|0|"), nil)
		if data == nil {
			db.Put([]byte("visit_dep|"+strconv.FormatUint(depth+1, 10)+"|"+url+"|0|"), []byte(url), nil)
			db.Put([]byte("visit_link|"+url+"|0|"), []byte(strconv.FormatUint(depth+1, 10)), nil)
		}
	}

	//_ = content
	use(content, db)
	// lets mark it as visited
	db.Put([]byte("visit_dep|"+strconv.FormatUint(depth, 10)+"|"+link+"|1|"), []byte(link), nil)
	db.Put([]byte("visit_link|"+link+"|1|"), []byte(strconv.FormatUint(depth, 10)), nil)
	db.Delete([]byte("visit_dep|"+strconv.FormatUint(depth, 10)+"|"+link+"|0|"), nil)
	db.Delete([]byte("visit_link|"+link+"|0|"), nil)
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

						solved := base.ResolveReference(u).String()

						if config.Crawler.Samedomain { // same domain only case
							u, err := url.Parse(solved)
							if err != nil {
								break
							}
							if u.Hostname() == base.Hostname() {
								dlinks = append(dlinks, solved)
							}
						} else {
							dlinks = append(dlinks, solved)
						}
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

			if exitcrawler {
				return
			}

			str := fmt.Sprint(strings.Join(words[i:j], " "))

			exists := true // let's see if it's already in database
			if data1, _ := db.Get([]byte("converted|"+str), nil); data1 == nil {
				if data2, _ := db.Get([]byte("testing|"+str), nil); data2 == nil {
					if data3, _ := db.Get([]byte("totest|"+str), nil); data3 == nil {
						if data4, _ := db.Get([]byte("result|"+str), nil); data4 == nil {
							exists = false
						}
					}
				}
			}

			if exists == false {
				err := db.Put([]byte("totest|"+str), []byte("1"), nil)
				if err != nil {
					fmt.Println("error writing crawl to db:", err.Error())
					return
				}
				atomic.AddUint64(&statsdbtotest, 1)
			}

			// computes 30% more lines than tests, to be sure there're always lines to be elaborated
			for config.Crawler.Autocrawlerspeed && float64(atomic.LoadUint64(&statsdbtotest)) > float64(atomic.LoadUint64(&statminutetests))*1.3 && atomic.LoadUint64(&statminutetests) != 0 {
				time.Sleep(10 * time.Millisecond)
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

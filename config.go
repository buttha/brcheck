package main

import (
	"flag"

	"github.com/BurntSushi/toml" // for configuration file
)

type configLog struct {
	Log        bool
	Logstdio   bool
	Logfile    string
	Lognet     bool
	Logstats   bool
	Logresult  bool
	Logcrawler bool
}

type configConn struct {
	Sslonly bool
	Tor     string
}

type configDb struct {
	Dbdir            string
	Exportdbfile     string
	Exportdbtable    string
	Exportdbinterval uint64
}

type configCore struct {
	Autobrainspeed bool
}

type configCrawler struct {
	Starturl         string
	Followlinks      int64
	Samedomain       bool
	Maxconcurrency   int64
	Iterator         uint64
	Autocrawlerspeed bool
}

// Config : configuration type
type Config struct {
	Log     configLog
	Conn    configConn
	Db      configDb
	Core    configCore
	Crawler configCrawler
}

// ParseConfig : reads command line params and config file
func ParseConfig() (Config, error) {

	var configuration Config

	paramConfigFile := flag.String("config", "", "config file. Command line parameters has higher priority")
	paramLogstdio := flag.Bool("logstdio", true, "log to standard output")
	paramLogfile := flag.String("logfile", "./brcheck.log", "log file. Set empty \"\" to disable")
	paramLognet := flag.Bool("lognet", false, "log network activity")
	paramLogstats := flag.Bool("logstats", true, "log activity stats")
	paramLogresult := flag.Bool("logresult", true, "log positive results")
	paramLogcrawler := flag.Bool("logcrawler", true, "log crawler's activity")
	paramSslonly := flag.Bool("sslonly", false, "connect only to SSL nodes")
	paramTor := flag.String("tor", "", "use tor. Leave empty \"\" to disable")
	paramDbdir := flag.String("dbdir", "", "working db directory")
	paramExportdbfile := flag.String("exportdbfile", "", "export database filename (sqlite3) leave empty to disable export")
	paramExportdbtable := flag.String("exportdbtable", "", "export db tablename")
	paramExportdbinterval := flag.Uint64("exportdbinterval", 0, "export every exportdbinterval seconds (0 to disable cron: db will be always exported when program stops)")
	paramAutobrainspeed := flag.Bool("autobrainspeed", true, "true = auto-adjust brainwallet's generation speed using electrum's test/second as parameter\nfalse = generate brainwallet at full speed (unnecessary high cpu usage)")
	paramStarturl := flag.String("starturl", "", "startup words extraction page (leave empty \"\" to disable crawling)")
	paramFollowlinks := flag.Int64("followlinks", 0, "follow found links in page: 0 = no ; -1 = infinte depth;  N = depth N")
	paramSamedomain := flag.Bool("samedomain", true, "follow same domain only urls (don't visit sites outside starturl)")
	paramMaxconcurrency := flag.Int64("maxconcurrency", 10, "max concurrent number of url fetched. WARNING: high memory usage")
	paramIterator := flag.Uint64("iterator", 10, "0 = disabled ; > 0 number of words")
	paramAutocrawlerspeed := flag.Bool("autocrawlerspeed", true, "true = auto-adjust generation speed using electrum's test/second as parameter\nfalse = generate at full speed (unnecessary high cpu usage)")
	flag.Parse()

	// set default values
	configuration.Log.Logstdio = *paramLogstdio
	configuration.Log.Logfile = *paramLogfile
	configuration.Log.Lognet = *paramLognet
	configuration.Log.Logstats = *paramLogstats
	configuration.Log.Logresult = *paramLogresult
	configuration.Log.Logcrawler = *paramLogcrawler
	configuration.Conn.Sslonly = *paramSslonly
	configuration.Conn.Tor = *paramTor
	configuration.Db.Dbdir = *paramDbdir
	configuration.Db.Exportdbfile = *paramExportdbfile
	configuration.Db.Exportdbtable = *paramExportdbtable
	configuration.Db.Exportdbinterval = *paramExportdbinterval
	configuration.Core.Autobrainspeed = *paramAutobrainspeed
	configuration.Crawler.Starturl = *paramStarturl
	configuration.Crawler.Followlinks = *paramFollowlinks
	configuration.Crawler.Samedomain = *paramSamedomain
	configuration.Crawler.Maxconcurrency = *paramMaxconcurrency
	configuration.Crawler.Iterator = *paramIterator
	configuration.Crawler.Autocrawlerspeed = *paramAutocrawlerspeed

	if *paramConfigFile != "" { // read config file
		if _, err := toml.DecodeFile(*paramConfigFile, &configuration); err != nil {
			return configuration, err
		}
	}

	// now rewrite configuration "visiting" flags that have been set via command line
	visitor := func(a *flag.Flag) {
		switch a.Name {
		case "logstdio":
			configuration.Log.Logstdio = *paramLogstdio
		case "logfile":
			configuration.Log.Logfile = *paramLogfile
		case "lognet":
			configuration.Log.Lognet = *paramLognet
		case "logstats":
			configuration.Log.Logstats = *paramLogstats
		case "logresult":
			configuration.Log.Logresult = *paramLogresult
		case "logcrawler":
			configuration.Log.Logcrawler = *paramLogcrawler
		case "sslonly":
			configuration.Conn.Sslonly = *paramSslonly
		case "tor":
			configuration.Conn.Tor = *paramTor
		case "dbdir":
			configuration.Db.Dbdir = *paramDbdir
		case "exportdbfile":
			configuration.Db.Exportdbfile = *paramExportdbfile
		case "exportdbtable":
			configuration.Db.Exportdbtable = *paramExportdbtable
		case "exportdbinterval":
			configuration.Db.Exportdbinterval = *paramExportdbinterval
		case "autobrainspeed":
			configuration.Core.Autobrainspeed = *paramAutobrainspeed
		case "starturl":
			configuration.Crawler.Starturl = *paramStarturl
		case "followlinks":
			configuration.Crawler.Followlinks = *paramFollowlinks
		case "samedomain":
			configuration.Crawler.Samedomain = *paramSamedomain
		case "maxconcurrency":
			configuration.Crawler.Maxconcurrency = *paramMaxconcurrency
		case "iterator":
			configuration.Crawler.Iterator = *paramIterator
		case "autocrawlerspeed":
			configuration.Crawler.Autocrawlerspeed = *paramAutocrawlerspeed
		}
	}
	flag.Visit(visitor)

	return configuration, nil
}

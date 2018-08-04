package main

import (
	"flag"

	"github.com/BurntSushi/toml" // for configuration file
)

type configLog struct {
	Lognet    bool
	Nostats   bool
	Logresult bool
}

type configConn struct {
	Resetconn int
}

type configDb struct {
	Dbdir            string
	Exportdbfile     string
	Exportdbtable    string
	Exportdbinterval uint64
}

// Config : configuration type
type Config struct {
	Log  configLog
	Conn configConn
	Db   configDb
}

// ParseConfig : reads command line params and config file
func ParseConfig() (Config, error) {

	var configuration Config

	paramConfigFile := flag.String("config", "", "config file. Command line parameters has higher priority")
	paramLognet := flag.Bool("lognet", true, "log network activity")
	paramNostats := flag.Bool("nostats", false, "don't log activity stats")
	paramLogresult := flag.Bool("logresult", true, "log positive results")
	paramResetconn := flag.Int("resetconn", 4000, "reset all connections after resetconn requests")
	paramDbdir := flag.String("dbdir", "", "working db directory")
	paramExportdbfile := flag.String("exportdbfile", "", "export database filename (sqlite3) leave empty to disable export")
	paramExportdbtable := flag.String("exportdbtable", "", "export db tablename")
	paramExportdbinterval := flag.Uint64("exportdbinterval", 0, "export every exportdbinterval seconds (0 to disable cron: db will be always exported when program stops)")
	flag.Parse()

	// set default values
	configuration.Log.Lognet = *paramLognet
	configuration.Log.Nostats = *paramNostats
	configuration.Log.Logresult = *paramLogresult
	configuration.Conn.Resetconn = *paramResetconn
	configuration.Db.Dbdir = *paramDbdir
	configuration.Db.Exportdbfile = *paramExportdbfile
	configuration.Db.Exportdbtable = *paramExportdbtable
	configuration.Db.Exportdbinterval = *paramExportdbinterval

	if *paramConfigFile != "" { // read config file
		if _, err := toml.DecodeFile(*paramConfigFile, &configuration); err != nil {
			return configuration, err
		}
	}

	// now rewrite configuration "visiting" flags that have been set via command line
	visitor := func(a *flag.Flag) {
		switch a.Name {
		case "lognet":
			configuration.Log.Lognet = *paramLognet
		case "nostats":
			configuration.Log.Nostats = *paramNostats
		case "logresult":
			configuration.Log.Logresult = *paramLogresult
		case "resetconn":
			configuration.Conn.Resetconn = *paramResetconn
		case "dbdir":
			configuration.Db.Dbdir = *paramDbdir
		case "exportdbfile":
			configuration.Db.Exportdbfile = *paramExportdbfile
		case "exportdbtable":
			configuration.Db.Exportdbtable = *paramExportdbtable
		case "exportdbinterval":
			configuration.Db.Exportdbinterval = *paramExportdbinterval
		}
	}
	flag.Visit(visitor)

	return configuration, nil
}

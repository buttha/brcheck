package main

import (
	"flag"

	"github.com/BurntSushi/toml" // for configuration file
)

type configLog struct {
	Lognet       bool
	Nostats      bool
	Logresult    bool
	Logbrainhash bool
}

type configConn struct {
	Maxconn   int
	Resetconn int
}

type configDb struct {
	Dbfile   string
	Dbprefix string
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
	paramMaxconn := flag.Int("maxconn", -1, "max electrum's connections (-1 = unlimited)")
	paramNostats := flag.Bool("nostats", false, "don't log activity stats")
	paramLogresult := flag.Bool("logresult", true, "log positive results")
	paramLogbrainhash := flag.Bool("logbrainhash", true, "log how many brain addresses are generated per second and per minute")
	paramResetconn := flag.Int("resetconn", 100, " reset connection with an electrum peer after N requests")
	paramDbFile := flag.String("dbfile", "", "database filename")
	paramDbPrefix := flag.String("dbprefix", "", "tablenames prefix")
	flag.Parse()

	// set default values
	configuration.Log.Lognet = *paramLognet
	configuration.Log.Nostats = *paramNostats
	configuration.Conn.Maxconn = *paramMaxconn
	configuration.Conn.Resetconn = *paramResetconn
	configuration.Db.Dbfile = *paramDbFile
	configuration.Log.Logresult = *paramLogresult
	configuration.Log.Logbrainhash = *paramLogbrainhash

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
		case "logbrainhash":
			configuration.Log.Logbrainhash = *paramLogbrainhash
		case "maxconn":
			configuration.Conn.Maxconn = *paramMaxconn
		case "resetconn":
			configuration.Conn.Resetconn = *paramResetconn
		case "dbfile":
			configuration.Db.Dbfile = *paramDbFile
		case "dbprefix":
			configuration.Db.Dbprefix = *paramDbPrefix
		}
	}
	flag.Visit(visitor)

	return configuration, nil
}

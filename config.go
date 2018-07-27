package main

import (
	"flag"

	"github.com/BurntSushi/toml" // for configuration file
)

type configLog struct {
	Lognet  bool
	Nostats bool
}

type configConn struct {
	Maxconn   int
	Resetconn int
}

// Config : configuration type
type Config struct {
	Log  configLog
	Conn configConn
}

// ParseConfig : reads command line params and config file
func ParseConfig() (Config, error) {

	var configuration Config

	var paramConfigFile *string // -config ./config : optional config file. Command line parameters has higher priority
	var paramLognet *bool       // -lognet : log network activity ( default: false )
	var paramMaxconn *int       // -maxconn 10 : max electrum's connections ( default: 100 )
	var paramNostats *bool      // -nostats : don't log activity stats
	var paramResetconn *int     // -resetconn 200 : reset connection with an electrum peer after paramResetconn requests ( default: 100 )
	// this is circumvent BANDWIDTH_LIMIT see http://electrumx.readthedocs.io/en/latest/environment.html#envvar-BANDWIDTH_LIMIT

	paramConfigFile = flag.String("config", "", "optional config file. Command line parameters has higher priority")
	paramLognet = flag.Bool("lognet", false, "log network activity")
	paramMaxconn = flag.Int("maxconn", 100, "max electrum's connections")
	paramNostats = flag.Bool("nostats", true, "don't log activity stats")
	paramResetconn = flag.Int("resetconn", 100, " reset connection with an electrum peer after N requests")
	flag.Parse()

	// set default values
	configuration.Log.Lognet = *paramLognet
	configuration.Log.Nostats = *paramNostats
	configuration.Conn.Maxconn = *paramMaxconn
	configuration.Conn.Resetconn = *paramResetconn

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
		case "maxconn":
			configuration.Conn.Maxconn = *paramMaxconn
		case "resetconn":
			configuration.Conn.Resetconn = *paramResetconn
		}
	}
	flag.Visit(visitor)

	return configuration, nil
}

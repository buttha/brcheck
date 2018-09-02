package main

import (
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buttha/electrum"
)

var mutexconnectedpeers = &sync.Mutex{}
var mutexdiscoveredpeers = &sync.Mutex{}
var mutexconnectingpeers = &sync.Mutex{} // avoid double connecting attempt

/*
startup peers ( from https://github.com/kyuupichan/electrumx/blob/master/electrumx/lib/coins.py )
Connect with these ones, then use server.peers.subscribe method
https://electrumx.readthedocs.io/en/latest/protocol-methods.html#server-peers-subscribe
to discover other peers
(I could use IRC, but not for now)
*/
var bootpeers = []electrum.Peer{
	electrum.Peer{Address: "", Name: "btc.smsys.me", Features: []string{"s995"}},
	electrum.Peer{Address: "", Name: "E-X.not.fyi", Features: []string{"s50002", "t50001"}},
	electrum.Peer{Address: "", Name: "elec.luggs.co", Features: []string{"s443"}},
	electrum.Peer{Address: "", Name: "electrum.vom-stausee.de", Features: []string{"s50002", "t50001"}},
	electrum.Peer{Address: "", Name: "electrum3.hachre.de", Features: []string{"s50002", "t50001"}},
	electrum.Peer{Address: "", Name: "electrum.hsmiths.com", Features: []string{"s50002", "t50001"}},
	electrum.Peer{Address: "", Name: "helicarrier.bauerj.eu", Features: []string{"s50002", "t50001"}},
	electrum.Peer{Address: "", Name: "electrum3.hachre.de", Features: []string{"s50002", "t50001"}},
	electrum.Peer{Address: "", Name: "node.arihanc.com", Features: []string{"s50002", "t50001"}},
}

type connectedpeer struct {
	peer          electrum.Peer
	connection    *electrum.Client
	wordschan     chan BrainAddress
	nottestedchan chan BrainAddress
	resultschan   chan BrainResult
}

var connectedpeers = make(map[string]connectedpeer)  // current connections
var discoveredpeers = make(map[string]electrum.Peer) // connected peers + connected peers' server.peers.subscribe
var connectingpeers = make(map[string]bool)          // peers under connection

// Establishconnections manages electrum's peers connection / comunication / channels
func Establishconnections(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {
	// connect on startup peers

	for _, peer := range bootpeers {

		mutexconnectingpeers.Lock()
		if connectingpeers[peer.Name] { // there's already a connection attempt: skip this peer
			mutexconnectingpeers.Unlock()
			continue
		}
		mutexconnectingpeers.Unlock()

		protoport := ""
		for _, feature := range peer.Features {
			if string(feature[0]) == "s" {
				protoport = feature
				break
			}
			if !config.Conn.Sslonly && string(feature[0]) == "t" {
				protoport = feature
			}
		}

		if protoport == "" { // config.Conn.Sslonly but peer has tcp only
			continue
		}

		mutexconnectingpeers.Lock()
		connectingpeers[peer.Name] = true
		mutexconnectingpeers.Unlock()

		go connect(peer, protoport, wordschan, nottestedchan, resultschan)

	}
}

// Keepconnections : reach and mantain max connection's number via peer discovery algo
func Keepconnections(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {
	var lastnumconnected, lastnumdiscovered int

	time.Sleep(time.Second * 5) // wait Establishconnections
	for {

		mutexdiscoveredpeers.Lock()
		mutexconnectedpeers.Lock()
		mutexconnectingpeers.Lock()

		for _, disco := range discoveredpeers { // search for a peer...

			if connectedpeers[disco.Name].connection != nil || connectingpeers[disco.Name] { // already connected or there's already a connection attempt
				continue
			}

			protoport := ""
			for _, feat := range disco.Features {
				if string(feat[0]) == "s" {
					protoport = feat
					break
				}
				if !config.Conn.Sslonly && string(feat[0]) == "t" {
					protoport = feat
				}
			}

			if protoport == "" { // config.Conn.Sslonly but peer has tcp only
				continue
			}

			connectingpeers[disco.Name] = true
			go connect(disco, protoport, wordschan, nottestedchan, resultschan)

		}

		mutexconnectingpeers.Unlock()
		mutexconnectedpeers.Unlock()
		mutexdiscoveredpeers.Unlock()

		for { // wait
			mutexconnectedpeers.Lock()
			mutexdiscoveredpeers.Lock()
			if len(connectedpeers) < lastnumconnected || len(discoveredpeers) > lastnumdiscovered {
				mutexconnectedpeers.Unlock()
				mutexdiscoveredpeers.Unlock()
				break
			}
			lastnumconnected = len(connectedpeers)
			lastnumdiscovered = len(discoveredpeers)
			mutexconnectedpeers.Unlock()
			mutexdiscoveredpeers.Unlock()
			time.Sleep(time.Second)
		}

		time.Sleep(time.Second)
	}
}

func connect(peer electrum.Peer, protoport string, wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {

	defer notconnecting(peer.Name)

	client, err := electrum.New(&electrum.Options{
		Address:   peer.Name + ":" + protoport[1:],
		Protocol:  "1.2",
		KeepAlive: true,
		TLS:       &tls.Config{InsecureSkipVerify: true},
		//Timeout:   5, // seconds
		Reconnect: false,
		Tor:       config.Conn.Tor,
	})

	if err != nil {
		mutexdiscoveredpeers.Lock()
		delete(discoveredpeers, peer.Name)
		mutexdiscoveredpeers.Unlock()
		return
	}

	_, err = client.ServerVersion()
	if err != nil {
		mutexdiscoveredpeers.Lock()
		delete(discoveredpeers, peer.Name)
		mutexdiscoveredpeers.Unlock()
		client.Close()
		return
	}

	feat, err := client.ServerFeatures()
	if err != nil {
		mutexdiscoveredpeers.Lock()
		delete(discoveredpeers, peer.Name)
		mutexdiscoveredpeers.Unlock()
		client.Close()
		return
	}

	protoversion, _ := strconv.ParseFloat(feat.ProtocolMax, 2)
	if protoversion < 1.2 { // no verbose mode for blockchain.transaction.get
		mutexdiscoveredpeers.Lock()
		delete(discoveredpeers, peer.Name)
		mutexdiscoveredpeers.Unlock()
		client.Close()
		return
	}

	peers, err := client.ServerPeers()
	if err != nil {
		mutexdiscoveredpeers.Lock()
		delete(discoveredpeers, peer.Name)
		mutexdiscoveredpeers.Unlock()
		client.Close()
		return
	}

	mutexconnectedpeers.Lock()

	connectedpeers[peer.Name] = connectedpeer{
		peer:          peer,
		connection:    client,
		wordschan:     wordschan,
		nottestedchan: nottestedchan,
		resultschan:   resultschan,
	}

	if config.Log.Lognet {
		logger(fmt.Sprintf("connected to: %s %s (# peers: %d)", peer.Name, protoport, len(connectedpeers)))
	}

	mutexconnectedpeers.Unlock()

	mutexdiscoveredpeers.Lock()

	discoveredpeers[peer.Name] = peer

	for _, dpeer := range peers {
		discoveredpeers[dpeer.Name] = electrum.Peer{
			Address:  dpeer.Address,
			Name:     dpeer.Name,
			Features: dpeer.Features,
		}
	}

	mutexdiscoveredpeers.Unlock()

	mutexconnectedpeers.Lock()
	go serveRequests(connectedpeers[peer.Name])
	mutexconnectedpeers.Unlock()

	return
}

func notconnecting(name string) {
	mutexconnectingpeers.Lock()
	delete(connectingpeers, name)
	mutexconnectingpeers.Unlock()
}

func serveRequests(peer connectedpeer) {
	var req BrainAddress

	// defer are executed LIFO
	defer deletepeer(peer)
	defer disconnect(peer)

	/* automatically disconnect afer 1 hour to circumvent BANDWIDTH_LIMIT
	see http://electrumx.readthedocs.io/en/latest/environment.html#envvar-BANDWIDTH_LIMIT
	*/
	start := time.Now()

	for {

		req = <-peer.wordschan

		balance, err := peer.connection.ScripthashBalance(req.Scripthash)
		//balance, err := peer.connection.AddressBalance(req.Address)
		if err != nil {
			servererr(peer, req, "AddressBalance", err)
			return
		}
		balanceC, err := peer.connection.ScripthashBalance(req.CompressedScripthash)
		//balanceC, err := peer.connection.AddressBalance(req.CompressedAddress)
		if err != nil {
			servererr(peer, req, "AddressBalanceCompressed", err)
			return
		}

		/*
			for "response too large" issue see
			https://github.com/kyuupichan/electrumx/issues/348
			https://github.com/spesmilo/electrum/issues/4315
		*/
		toolarge := false
		history, err := peer.connection.ScripthashHistory(req.Scripthash)
		//history, err := peer.connection.AddressHistory(req.Address)
		if err != nil {
			if strings.HasPrefix(err.Error(), "response too large") {
				toolarge = true
			} else {
				servererr(peer, req, "AddressHistory", err)
				return
			}
		}

		toolargeC := false
		historyC, err := peer.connection.ScripthashHistory(req.CompressedScripthash)
		//historyC, err := peer.connection.AddressHistory(req.CompressedAddress)
		if err != nil {
			if strings.HasPrefix(err.Error(), "response too large") {
				toolargeC = true
			} else {
				servererr(peer, req, "AddressHistoryCompressed", err)
				return
			}
		}

		var ltime time.Time
		var numtx int
		if !toolarge {
			if len(*history) > 0 {
				numtx = len(*history)
				last := (*history)[len(*history)-1]
				lasttx, err := peer.connection.GetTransactionVerbose(last.Hash)
				if err != nil {
					servererr(peer, req, "GetTransactionVerbose", err)
					return
				}
				l := lasttx.(map[string]interface{})
				ltime = time.Unix(int64(l["time"].(float64)), 0)
			}
		}

		var ltimeC time.Time
		var numtxC int
		if !toolargeC {
			if len(*historyC) > 0 {
				numtxC = len(*historyC)
				lastC := (*historyC)[len(*historyC)-1]
				lasttxC, err := peer.connection.GetTransactionVerbose(lastC.Hash)
				if err != nil {
					servererr(peer, req, "GetTransactionVerbose Compressed", err)
					return
				}
				lc := lasttxC.(map[string]interface{})
				ltimeC = time.Unix(int64(lc["time"].(float64)), 0)
			}
		}

		if toolarge {
			numtx = -1
		}
		if toolargeC {
			numtxC = -1
		}

		peer.resultschan <- BrainResult{
			Address:               req,
			Confirmed:             balance.Confirmed,
			Unconfirmed:           balance.Unconfirmed,
			LastTxTime:            ltime,
			NumTx:                 numtx,
			ConfirmedCompressed:   balanceC.Confirmed,
			UnconfirmedCompressed: balanceC.Unconfirmed,
			LastTxTimeCompressed:  ltimeC,
			NumTxCompressed:       numtxC,
		}

		if time.Since(start) >= time.Hour {
			if config.Log.Lognet {
				logger(fmt.Sprintf("Disconnected from: %s: reached 1 hour connection", peer.peer.Name))
			}
			return
		}

	}

}

func servererr(peer connectedpeer, req BrainAddress, operation string, err error) {
	peer.nottestedchan <- req // send back request so another server will serve it
	if config.Log.Lognet {
		mutexconnectedpeers.Lock()
		logger(fmt.Sprintf("Disconnected from: %s (# peers: %d) requesting \"%s\" on address %+v err: %s", peer.peer.Name, len(connectedpeers)-1, operation, req, err))
		mutexconnectedpeers.Unlock()
	}
}

func deletepeer(peer connectedpeer) {
	mutexconnectedpeers.Lock()
	delete(connectedpeers, peer.peer.Name)
	mutexconnectedpeers.Unlock()
}

func disconnect(peer connectedpeer) {
	if peer.connection != nil {
		peer.connection.Close()
	}
}

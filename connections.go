package main

import (
	"crypto/tls"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buttha/electrum"
)

var mutex = &sync.Mutex{}

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

// Establishconnections manages electrum's peers connection / comunication / channels
func Establishconnections(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {

	done := make(chan bool)
	num := 0

	// connect on startup peers (SSL only)
	for _, peer := range bootpeers {
		for _, feature := range peer.Features {
			if strings.HasPrefix(feature, "s") { // server supports SSL
				num++
				go connect(peer, strings.TrimPrefix(feature, "s"), wordschan, nottestedchan, resultschan, done)
			}
		}
	}
	// wait connect calls
	for {
		<-done
		num--
		if num == 0 {
			break
		}
	}

	newconn := 0
	num = 0
	// go on in order to reach and mantain paramMaxconn connections
	for {
		if len(connectedpeers) < config.Conn.Maxconn || config.Conn.Maxconn == -1 {
			newconn = config.Conn.Maxconn - len(connectedpeers) // how many new connections to establish
			for _, disco := range discoveredpeers {             // search for a peer...
				if connectedpeers[disco.Name].connection == nil { // .... not already connected...
					for _, feat := range disco.Features { // ... which supports...
						if strings.HasPrefix(feat, "s") { // ... SSL
							newconn--
							num++
							go connect(disco, strings.TrimPrefix(feat, "s"), wordschan, nottestedchan, resultschan, done)
						}
					}
				}
				if newconn == 0 && config.Conn.Maxconn != -1 {
					break
				}
			}
			// wait connect calls
			for {
				<-done
				num--
				if num == 0 {
					break
				}
			}
		}
	}

}

func connect(peer electrum.Peer, port string, wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult, done chan bool) {

	var tlsconfig tls.Config
	tlsconfig.InsecureSkipVerify = true

	client, err := electrum.New(&electrum.Options{
		Address:   peer.Name + ":" + port,
		Protocol:  "1.2",
		KeepAlive: true,
		TLS:       &tlsconfig,
	})

	if err != nil {
		done <- true
		return
	}

	_, err = client.ServerVersion()
	if err != nil {
		done <- true
		return
	}

	feat, err := client.ServerFeatures()
	if err != nil {
		client.Close()
		done <- true
		return
	}

	protoversion, _ := strconv.ParseFloat(feat.ProtocolMax, 2)
	if protoversion < 1.2 { // no verbose mode for blockchain.transaction.get
		client.Close()
		done <- true
		return
	}

	peers, err := client.ServerPeers()
	if err != nil {
		client.Close()
		done <- true
		return
	}

	mutex.Lock()
	connectedpeers[peer.Name] = connectedpeer{
		peer:          peer,
		connection:    client,
		wordschan:     wordschan,
		nottestedchan: nottestedchan,
		resultschan:   resultschan,
	}

	if config.Log.Lognet {
		log.Printf("connected to: %s (# peers: %d)", peer.Name, len(connectedpeers))
	}

	discoveredpeers[peer.Name] = peer

	for _, dpeer := range peers {
		discoveredpeers[dpeer.Name] = electrum.Peer{
			Address:  dpeer.Address,
			Name:     dpeer.Name,
			Features: dpeer.Features,
		}
	}
	mutex.Unlock()

	go serveRequests(connectedpeers[peer.Name])

	done <- true
	return

}

func serveRequests(peer connectedpeer) {
	var req BrainAddress

	// defer are executed LIFO
	defer deletepeer(peer)
	defer peer.connection.Close()

	numrequests := 0

	for {
		req = <-peer.wordschan

		//balance, err := peer.connection.ScripthashBalance(req.Scripthash)
		balance, err := peer.connection.AddressBalance(req.Address)
		if err != nil {
			servererr(peer, req, "AddressBalance", err)
			return
		}
		//balanceC, err := peer.connection.ScripthashBalance(req.CompressedScripthash)
		balanceC, err := peer.connection.AddressBalance(req.CompressedAddress)
		if err != nil {
			servererr(peer, req, "AddressBalanceCompressed", err)
			return
		}

		//history, err := peer.connection.ScripthashHistory(req.Scripthash)

		/*
			for "response too large" issue see
			https://github.com/kyuupichan/electrumx/issues/348
			https://github.com/spesmilo/electrum/issues/4315
		*/
		toolarge := false
		history, err := peer.connection.AddressHistory(req.Address)
		if err != nil {
			if strings.HasPrefix(err.Error(), "response too large") {
				toolarge = true
			} else {
				servererr(peer, req, "AddressHistory", err)
				return
			}
		}

		//historyC, err := peer.connection.ScripthashHistory(req.CompressedScripthash)
		toolargeC := false
		historyC, err := peer.connection.AddressHistory(req.CompressedAddress)
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

		numrequests++

		// reset connection if maximum request's number is reached
		if numrequests > config.Conn.Resetconn {
			if config.Log.Lognet {
				log.Printf("Disconnected from: %s (# peers: %d): reached %d requests", peer.peer.Name, len(connectedpeers)-1, config.Conn.Resetconn)
			}
			return
		}

	}

}

func servererr(peer connectedpeer, req BrainAddress, operation string, err error) {
	peer.nottestedchan <- req // send back request so another server will serve it
	if config.Log.Lognet {
		log.Printf("Disconnected from: %s (# peers: %d) requesting \"%s\" on address %+v err: %s", peer.peer.Name, len(connectedpeers)-1, operation, req, err)
	}
}

func deletepeer(peer connectedpeer) {
	mutex.Lock()
	delete(connectedpeers, peer.peer.Name)
	mutex.Unlock()
}

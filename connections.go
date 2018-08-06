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
	closeconn     chan bool // to force disconnection
}

var connectedpeers = make(map[string]connectedpeer)  // current connections
var discoveredpeers = make(map[string]electrum.Peer) // connected peers + connected peers' server.peers.subscribe
var connectingpeers = make(map[string]bool)          // peers under connection

// Establishconnections manages electrum's peers connection / comunication / channels
func Establishconnections(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {
	//done := make(chan bool)
	num := 0
	// connect on startup peers (SSL only)
	for _, peer := range bootpeers {
		for _, feature := range peer.Features {
			mutexconnectingpeers.Lock()
			if strings.HasPrefix(feature, "s") && !connectingpeers[peer.Name] { // server supports SSL
				connectingpeers[peer.Name] = true
				num++
				go connect(peer, strings.TrimPrefix(feature, "s"), wordschan, nottestedchan, resultschan /*, done */)
			}
			mutexconnectingpeers.Unlock()
		}
	}
	/*
		if num > 0 {
			for {
				<-done
				num--
				if num == 0 {
					break
				}
			}
		}
	*/
}

// Keepconnections : reach and mantain max connection's number via peer discovery algo
func Keepconnections(wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult) {
	var num int64
	time.Sleep(time.Second * 5) // wait Establishconnections
	//done := make(chan bool)
	for {
		mutexdiscoveredpeers.Lock()
		mutexconnectedpeers.Lock()
		mutexconnectingpeers.Lock()
		num = 0
		for _, disco := range discoveredpeers { // search for a peer...
			if connectedpeers[disco.Name].connection == nil && !connectingpeers[disco.Name] { // .... not already connected...
				for _, feat := range disco.Features { // ... which supports...
					if strings.HasPrefix(feat, "s") { // ... SSL
						connectingpeers[disco.Name] = true
						num++
						go connect(disco, strings.TrimPrefix(feat, "s"), wordschan, nottestedchan, resultschan /*, done */)
					}

				}
			}
		}
		mutexconnectingpeers.Unlock()
		mutexconnectedpeers.Unlock()
		mutexdiscoveredpeers.Unlock()

		time.Sleep(time.Second)
		/*
			if num > 0 {
				for {
					<-done
					num--
					if num == 0 {
						break
					}
				}
			}
		*/
	}
}

// Resetconnections : reset all connections
func Resetconnections() {
	if config.Log.Lognet {
		log.Println("resetting all connections")
	}
	for _, peer := range connectedpeers {
		if peer.connection != nil {
			peer.closeconn <- true
		}
	}
}

func connect(peer electrum.Peer, port string, wordschan, nottestedchan chan BrainAddress, resultschan chan BrainResult /*, done chan bool*/) {

	//defer doneconnect(done)
	defer notconnecting(peer.Name)

	var tlsconfig tls.Config
	tlsconfig.InsecureSkipVerify = true

	client, err := electrum.New(&electrum.Options{
		Address:   peer.Name + ":" + port,
		Protocol:  "1.2",
		KeepAlive: true,
		TLS:       &tlsconfig,
		Timeout:   5, // seconds
		// Reconnect: false,

	})

	if err != nil {
		return
	}

	_, err = client.ServerVersion()
	if err != nil {
		client.Close()
		return
	}

	feat, err := client.ServerFeatures()
	if err != nil {
		client.Close()
		return
	}

	protoversion, _ := strconv.ParseFloat(feat.ProtocolMax, 2)
	if protoversion < 1.2 { // no verbose mode for blockchain.transaction.get
		client.Close()
		return
	}

	peers, err := client.ServerPeers()
	if err != nil {
		client.Close()
		return
	}

	mutexconnectedpeers.Lock()

	close := make(chan bool, 1) // buffered, so I don't have to wait connection shutdown

	connectedpeers[peer.Name] = connectedpeer{
		peer:          peer,
		connection:    client,
		wordschan:     wordschan,
		nottestedchan: nottestedchan,
		resultschan:   resultschan,
		closeconn:     close,
	}

	mutexconnectedpeers.Unlock()

	if config.Log.Lognet {
		log.Printf("connected to: %s (# peers: %d)", peer.Name, len(connectedpeers))
	}

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

	go serveRequests(connectedpeers[peer.Name])

	return
}

/*
func doneconnect(done chan bool) {
	done <- true
}
*/
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

	numrequests := 0

	for {

		// check if disconnetion is required
		select {
		case <-peer.closeconn:
			if config.Log.Lognet {
				log.Printf("disconnected from: %s (# peers: %d): forced connection shutdown", peer.peer.Name, len(connectedpeers)-1)
			}
			return
		default:
		}

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
		if numrequests >= config.Conn.Resetsingleconn {
			if config.Log.Lognet {
				log.Printf("Disconnected from: %s (# peers: %d): reached %d requests", peer.peer.Name, len(connectedpeers)-1, config.Conn.Resetsingleconn)
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
	mutexconnectedpeers.Lock()
	delete(connectedpeers, peer.peer.Name)
	mutexconnectedpeers.Unlock()
}

func disconnect(peer connectedpeer) {
	if peer.connection != nil {
		peer.connection.Close()
	}
}

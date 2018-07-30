package main

import (
	"crypto/sha256"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil"
)

// BrainAddress contains compressed and uncompressed key/address
type BrainAddress struct {
	Passphrase           string
	Address              string
	PrivkeyWIF           string
	CompressedAddress    string
	CompressedPrivkeyWIF string
	/*
		To do. See https://electrumx.readthedocs.io/en/latest/protocol-basics.html#script-hashes

		ScriptPubkey           string
		CompressedScriptPubkey string
		Scripthash             string // reverse_string(sha256(ScriptPubKey))
		CompressedScripthash   string // reverse_string(sha256(CompressedScriptPubKey))
	*/
}

// BrainGenerator generates compressed and uncompressed key/address from input string
func BrainGenerator(passphrase string) BrainAddress {

	var brain BrainAddress

	sh := sha256.Sum256([]byte(passphrase)) // sha256

	privKey, pubKey := btcec.PrivKeyFromBytes(btcec.S256(), sh[:])
	wif, _ := btcutil.NewWIF(privKey, &chaincfg.MainNetParams, false) // brain.PrivkeyWIF
	wifC, _ := btcutil.NewWIF(privKey, &chaincfg.MainNetParams, true) // brain.CompressedPrivkeyWIF
	addr, _ := btcutil.NewAddressPubKey(pubKey.SerializeUncompressed(), &chaincfg.MainNetParams)
	addrC, _ := btcutil.NewAddressPubKey(pubKey.SerializeCompressed(), &chaincfg.MainNetParams)

	brain.Passphrase = passphrase
	brain.Address = addr.EncodeAddress()
	brain.PrivkeyWIF = wif.String()
	brain.CompressedAddress = addrC.EncodeAddress()
	brain.CompressedPrivkeyWIF = wifC.String()

	return brain
}

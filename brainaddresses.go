package main

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
)

// BrainAddress contains compressed and uncompressed key/address
type BrainAddress struct {
	Passphrase           string
	Address              string
	Scripthash           string
	PrivkeyWIF           string
	CompressedAddress    string
	CompressedScripthash string
	CompressedPrivkeyWIF string
}

// BrainGenerator generates compressed and uncompressed key/address from input string
func BrainGenerator(passphrase string) BrainAddress {

	var brain BrainAddress

	sh := sha256.Sum256([]byte(passphrase)) // sha256

	privKey, pubKey := btcec.PrivKeyFromBytes(btcec.S256(), sh[:])
	wif, _ := btcutil.NewWIF(privKey, &chaincfg.MainNetParams, false)
	wifC, _ := btcutil.NewWIF(privKey, &chaincfg.MainNetParams, true)
	addr, _ := btcutil.NewAddressPubKey(pubKey.SerializeUncompressed(), &chaincfg.MainNetParams)
	addrC, _ := btcutil.NewAddressPubKey(pubKey.SerializeCompressed(), &chaincfg.MainNetParams)

	brain.Passphrase = passphrase
	brain.Address = addr.EncodeAddress()
	brain.PrivkeyWIF = wif.String()
	brain.CompressedAddress = addrC.EncodeAddress()
	brain.CompressedPrivkeyWIF = wifC.String()

	/* now let's generate script hashes
	see https://electrumx.readthedocs.io/en/latest/protocol-basics.html#script-hashes
	*/
	baddr, _ := btcutil.DecodeAddress(brain.Address, &chaincfg.MainNetParams)
	baddrC, _ := btcutil.DecodeAddress(brain.CompressedAddress, &chaincfg.MainNetParams)

	// P2PKH scripts
	// P.S. OP codes verions are: disasm, err := txscript.DisasmString(script)
	script, _ := txscript.PayToAddrScript(baddr)
	scriptC, _ := txscript.PayToAddrScript(baddrC)

	// low let's convert in electrum's script hases:
	// a) sha256
	sha256script := sha256.Sum256(script)
	sha256scriptC := sha256.Sum256(scriptC)
	// b) "reversed"
	for i := 0; i < len(sha256script)/2; i++ {
		j := len(sha256script) - i - 1
		sha256script[i], sha256script[j] = sha256script[j], sha256script[i]
	}
	for i := 0; i < len(sha256scriptC)/2; i++ {
		j := len(sha256scriptC) - i - 1
		sha256scriptC[i], sha256scriptC[j] = sha256scriptC[j], sha256scriptC[i]
	}

	brain.Scripthash = hex.EncodeToString(sha256script[:])
	brain.CompressedScripthash = hex.EncodeToString(sha256scriptC[:])

	return brain
}

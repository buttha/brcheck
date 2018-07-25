package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/buttha/btckeygenie/btckey"
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

	dst := make([]byte, hex.EncodedLen(len(sh)))
	hex.Encode(dst, sh[:])
	hash := string(dst) // hex(sha256)

	d := new(big.Int)
	d, _ = d.SetString(hash, 16) // bigint(hex(sha256))

	key := btckey.NewPrivateKey(d)

	brain.Passphrase = passphrase
	brain.Address = key.ToAddressUncompressed()
	brain.PrivkeyWIF = key.ToWIF()
	brain.CompressedAddress = key.ToAddress()
	brain.CompressedPrivkeyWIF = key.ToWIFC()

	/*
		pubbytesuncompressed := key.PublicKey.ToBytesUncompressed()
		pubbytesuncompressedstr := byteString(pubbytesuncompressed)

		brain.PublicKey = pubbytesuncompressedstr[0:65]

		pubbytescompressed := key.PublicKey.ToBytes()

		brain.CompressedPublicKey = byteString(pubbytescompressed)

		brain.Scripthash = scripthash(brain.PublicKey)
		brain.CompressedScripthash = scripthash(brain.CompressedPublicKey)
	*/

	return brain
}

func byteString(b []byte) (s string) {
	s = ""
	for i := 0; i < len(b); i++ {
		s += fmt.Sprintf("%02X", b[i])
	}
	return s
}

/*
func scripthash(s string) string {
	return reverse(scriptPubKey(s))
}

func reverse(s []byte) (result string) { // see https://electrumx.readthedocs.io/en/latest/protocol-basics.html#script-hashes

	var res []byte

	for i := len(s) - 2; i >= 0; i = i - 2 {
		res = append(res, s[i])
		res = append(res, s[i+1])
	}

	return string(res)
}
*/

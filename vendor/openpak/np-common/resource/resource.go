package resource

import (
	"crypto/hmac"
	"crypto/sha1"
	"fmt"
)

// VSH reads a 65-byte signature comment plus LF before the XML. The MAC covers
// only the XML, zero-padded to a 64-byte boundary (FUN_0022cd90 / 0022c698).
func Sign(body, key []byte) []byte {
	padded := make([]byte, (len(body)+63)&^63)
	copy(padded, body)
	mac := hmac.New(sha1.New, key)
	mac.Write(padded)
	return append([]byte(fmt.Sprintf("<!--Sce-Np-Signature: %x-->\n", mac.Sum(nil))), body...)
}

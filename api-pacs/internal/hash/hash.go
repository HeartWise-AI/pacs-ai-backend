package hash

import (
	"crypto/md5"
	"encoding/hex"
)

// GetMD5Hash returns the MD5 hash of a string
func GetMD5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))

	return hex.EncodeToString(hasher.Sum(nil))
}

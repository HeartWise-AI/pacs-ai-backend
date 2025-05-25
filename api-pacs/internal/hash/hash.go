package hash

import (
	"crypto/md5"
	"encoding/hex"
	"hash/crc32"
	"strconv"
)

// GetCRC32DigitHash returns the CRC32 hash of a string as a pure digit string
func GetCRC32DigitHash(text string) string {
	// CRC32 to pure digits (no hex)
	return strconv.FormatUint(uint64(crc32.ChecksumIEEE([]byte(text))), 10)
}

// GetMD5Hash returns the MD5 hash of a string
func GetMD5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))

	return hex.EncodeToString(hasher.Sum(nil))
}

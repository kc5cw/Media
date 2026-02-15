package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

func ComputeHashes(filePath string) (crcHex string, shaHex string, err error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	crc := crc32.NewIEEE()
	sha := sha256.New()
	if _, err := io.Copy(io.MultiWriter(crc, sha), f); err != nil {
		return "", "", err
	}

	crcHex = fmt.Sprintf("%08x", crc.Sum32())
	shaHex = hex.EncodeToString(sha.Sum(nil))
	return crcHex, shaHex, nil
}

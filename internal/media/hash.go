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
	return ComputeHashesWithProgress(filePath, nil)
}

func ComputeHashesWithProgress(filePath string, onProgress func(int64)) (crcHex string, shaHex string, err error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	crc := crc32.NewIEEE()
	sha := sha256.New()
	src := io.Reader(f)
	if onProgress != nil {
		src = &hashProgressReader{r: f, onProgress: onProgress}
	}
	buf := make([]byte, 1024*1024)
	if _, err := io.CopyBuffer(io.MultiWriter(crc, sha), src, buf); err != nil {
		return "", "", err
	}

	crcHex = fmt.Sprintf("%08x", crc.Sum32())
	shaHex = hex.EncodeToString(sha.Sum(nil))
	return crcHex, shaHex, nil
}

type hashProgressReader struct {
	r          io.Reader
	onProgress func(int64)
}

func (p *hashProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.onProgress != nil {
		p.onProgress(int64(n))
	}
	return n, err
}

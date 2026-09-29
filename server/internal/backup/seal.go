// Package backup provides bounded, authenticated offline backup primitives.
// It never discovers credentials, changes a live database, or chooses a tenant.
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

var ErrInvalid = errors.New("BACKUP_INVALID_OR_UNCONFIRMED")

const chunkSize = 1 << 20
const MaxFileSize int64 = 64 << 30
const magic = "FGBK0001"

func fileCipher(key, header []byte) (cipher.AEAD, error) {
	if len(key) != 32 || len(header) != len(magic)+32 || string(header[:len(magic)]) != magic {
		return nil, ErrInvalid
	}
	subkey, e := hkdf.Key(sha256.New, key, header[len(magic):], "frogim/backup/file/v1", 32)
	if e != nil {
		return nil, ErrInvalid
	}
	b, e := aes.NewCipher(subkey)
	if e != nil {
		return nil, ErrInvalid
	}
	return cipher.NewGCM(b)
}

// Every file has a random 256-bit salt and its own derived AES-256-GCM key.
// Chunk number, header and final-record flag are authenticated. A mandatory
// authenticated empty final record detects truncation even at chunk boundaries.
func Encrypt(dst io.Writer, src io.Reader, key []byte) error {
	header := make([]byte, len(magic)+32)
	copy(header, magic)
	if _, e := rand.Read(header[len(magic):]); e != nil {
		return ErrInvalid
	}
	aead, e := fileCipher(key, header)
	if e != nil {
		return e
	}
	if e = writeFull(dst, header); e != nil {
		return e
	}
	buf := make([]byte, chunkSize)
	var index uint64
	var total int64
	for {
		n, err := io.ReadFull(src, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		if n > 0 {
			total += int64(n)
			if total > MaxFileSize {
				return ErrInvalid
			}
			if e = sealChunk(dst, aead, header, index, buf[:n], false); e != nil {
				return e
			}
			index++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return sealChunk(dst, aead, header, index, nil, true)
		}
	}
}

func chunkContext(header []byte, index uint64, final bool) ([]byte, []byte) {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], index)
	aad := append(append([]byte{}, header...), nonce...)
	if final {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 0)
	}
	return nonce, aad
}
func sealChunk(dst io.Writer, aead cipher.AEAD, header []byte, index uint64, plain []byte, final bool) error {
	nonce, aad := chunkContext(header, index, final)
	sealed := aead.Seal(nil, nonce, plain, aad)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(sealed)))
	if e := writeFull(dst, size[:]); e != nil {
		return e
	}
	return writeFull(dst, sealed)
}
func writeFull(dst io.Writer, data []byte) error {
	n, e := dst.Write(data)
	if e == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return e
}
func Decrypt(dst io.Writer, src io.Reader, key []byte) error {
	header := make([]byte, len(magic)+32)
	if _, e := io.ReadFull(src, header); e != nil {
		return ErrInvalid
	}
	aead, e := fileCipher(key, header)
	if e != nil {
		return e
	}
	var index uint64
	var total int64
	for {
		var n uint32
		if binary.Read(src, binary.BigEndian, &n) != nil || n < uint32(aead.Overhead()) || n > chunkSize+uint32(aead.Overhead()) {
			return ErrInvalid
		}
		sealed := make([]byte, n)
		if _, e = io.ReadFull(src, sealed); e != nil {
			return ErrInvalid
		}
		final := n == uint32(aead.Overhead())
		nonce, aad := chunkContext(header, index, final)
		plain, e := aead.Open(nil, nonce, sealed, aad)
		if e != nil {
			return ErrInvalid
		}
		if final {
			var extra [1]byte
			if n, e := src.Read(extra[:]); n != 0 || e != io.EOF {
				return ErrInvalid
			}
			return nil
		}
		total += int64(len(plain))
		if total > MaxFileSize {
			return ErrInvalid
		}
		if e = writeFull(dst, plain); e != nil {
			return e
		}
		index++
	}
}

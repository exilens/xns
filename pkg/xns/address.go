package xns

import (
	"encoding/hex"
	"errors"
	"math/big"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/sha3"
)

const (
	moneroBase58Alphabet        = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	moneroBase58FullBlockSize   = 8
	moneroBase58FullEncodedSize = 11
)

var moneroBase58EncodedBlockSizes = [...]int{0, 2, 3, 5, 6, 7, 9, 10, 11}

func deriveProtocolAddress(prefix uint64) (string, error) {
	spend, err := hex.DecodeString(ProtocolSpendPublicKey)
	if err != nil {
		return "", err
	}
	view, err := ProtocolViewPublicKey()
	if err != nil {
		return "", err
	}
	body := append(encodeVarint(prefix), spend...)
	body = append(body, view...)
	checksum := keccak256(body)[:4]
	return moneroBase58Encode(append(body, checksum...))
}

func ProtocolViewPublicKey() ([]byte, error) {
	raw, err := hex.DecodeString(ProtocolViewSecret)
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, errors.New("protocol view secret must be 32 bytes")
	}
	scalar, err := new(edwards25519.Scalar).SetCanonicalBytes(raw)
	if err != nil {
		return nil, err
	}
	return new(edwards25519.Point).ScalarBaseMult(scalar).Bytes(), nil
}

func encodeVarint(n uint64) []byte {
	var out []byte
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func moneroBase58Encode(data []byte) (string, error) {
	out := make([]byte, 0, len(data)*moneroBase58FullEncodedSize/moneroBase58FullBlockSize+2)
	for offset := 0; offset < len(data); offset += moneroBase58FullBlockSize {
		end := offset + moneroBase58FullBlockSize
		if end > len(data) {
			end = len(data)
		}
		block, err := moneroBase58EncodeBlock(data[offset:end])
		if err != nil {
			return "", err
		}
		out = append(out, block...)
	}
	return string(out), nil
}

func moneroBase58EncodeBlock(block []byte) ([]byte, error) {
	if len(block) >= len(moneroBase58EncodedBlockSizes) {
		return nil, errors.New("base58 block too large")
	}
	size := moneroBase58EncodedBlockSizes[len(block)]
	value := new(big.Int).SetBytes(block)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	rem := new(big.Int)
	out := make([]byte, size)
	for i := range out {
		out[i] = '1'
	}
	for i := size - 1; i >= 0 && value.Cmp(zero) > 0; i-- {
		value.DivMod(value, base, rem)
		out[i] = moneroBase58Alphabet[rem.Int64()]
	}
	if value.Cmp(zero) != 0 {
		return nil, errors.New("base58 block overflow")
	}
	return out, nil
}

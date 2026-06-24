package monero

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"filippo.io/edwards25519"
	"git.gammaspectra.live/P2Pool/consensus/v5/monero/cryptonight"
	"golang.org/x/crypto/sha3"

	"github.com/exilens/xns/pkg/xns"
)

const (
	unsignedTxPrefix = "Monero unsigned tx set\x05"
	chachaIVSize     = 8
	moneroSigSize    = 64
)

var (
	defaultEncryptedPaymentIDNonce = []byte{0x02, 0x09, 0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	edwardsOrder, _                = new(big.Int).SetString("1000000000000000000000000000000014def9dea2f79cd65812631a5cf5d3ed", 16)
)

type TxSetPatch struct {
	ExtraLengthOffset int    `json:"extra_length_offset"`
	OldExtraLength    int    `json:"old_extra_length"`
	NewExtraLength    int    `json:"new_extra_length"`
	PayloadHex        string `json:"payload_hex"`
	NewExtraHex       string `json:"new_extra_hex"`
	InputSize         int    `json:"input_size"`
	OutputSize        int    `json:"output_size"`
}

func PatchUnsignedTxSetHex(unsignedHex, viewSecretHex string, payload [xns.PayloadSize]byte) (string, TxSetPatch, error) {
	blob, err := hex.DecodeString(unsignedHex)
	if err != nil {
		return "", TxSetPatch{}, err
	}
	viewSecret, err := decodeSecret(viewSecretHex)
	if err != nil {
		return "", TxSetPatch{}, err
	}
	nonce, plain, err := decryptUnsignedTxSet(blob, viewSecret)
	if err != nil {
		return "", TxSetPatch{}, err
	}
	patchedPlain, meta, err := patchTxSetPlaintext(plain, payload[:])
	if err != nil {
		return "", TxSetPatch{}, err
	}
	patched, err := encryptUnsignedTxSet(patchedPlain, viewSecret, nonce)
	if err != nil {
		return "", TxSetPatch{}, err
	}
	meta.InputSize = len(blob)
	meta.OutputSize = len(patched)
	return hex.EncodeToString(patched), meta, nil
}

func decodeSecret(secretHex string) ([]byte, error) {
	raw, err := hex.DecodeString(secretHex)
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, errors.New("secret key must be 32 bytes")
	}
	if _, err := scalarFromLE(raw); err != nil {
		return nil, fmt.Errorf("invalid secret key scalar: %w", err)
	}
	return raw, nil
}

func decryptUnsignedTxSet(blob, viewSecret []byte) ([]byte, []byte, error) {
	if !bytes.HasPrefix(blob, []byte(unsignedTxPrefix)) {
		return nil, nil, errors.New("input does not look like a Monero unsigned tx set")
	}
	encrypted := blob[len(unsignedTxPrefix):]
	if len(encrypted) < chachaIVSize+moneroSigSize {
		return nil, nil, errors.New("encrypted unsigned tx set is too short")
	}
	nonce := encrypted[:chachaIVSize]
	ciphertext := encrypted[chachaIVSize : len(encrypted)-moneroSigSize]
	plain := append([]byte(nil), ciphertext...)
	chacha20XOR(plain, plain, chachaKey(viewSecret), nonce)
	return append([]byte(nil), nonce...), plain, nil
}

func encryptUnsignedTxSet(plain, viewSecret, nonce []byte) ([]byte, error) {
	if len(nonce) != chachaIVSize {
		return nil, errors.New("chacha nonce must be 8 bytes")
	}
	ciphertext := append([]byte(nil), plain...)
	chacha20XOR(ciphertext, ciphertext, chachaKey(viewSecret), nonce)
	encrypted := append(append([]byte(nil), nonce...), ciphertext...)
	sig, err := moneroSignature(fastHash(encrypted), viewSecret)
	if err != nil {
		return nil, err
	}
	out := append([]byte(unsignedTxPrefix), encrypted...)
	out = append(out, sig...)
	return out, nil
}

func patchTxSetPlaintext(plain, payload []byte) ([]byte, TxSetPatch, error) {
	if len(payload) != xns.PayloadSize {
		return nil, TxSetPatch{}, fmt.Errorf("payload must be %d bytes", xns.PayloadSize)
	}
	lengthOffset, oldExtra, err := locateDefaultExtra(plain)
	if err != nil {
		return nil, TxSetPatch{}, err
	}
	txPubKey := oldExtra[:33]
	if txPubKey[0] != xns.TxExtraPubKey {
		return nil, TxSetPatch{}, errors.New("matched extra does not start with a tx public key tag")
	}
	newExtra := append(append([]byte(nil), txPubKey...), xns.TxExtraNonce, byte(len(payload)))
	newExtra = append(newExtra, payload...)
	if len(newExtra) > 127 {
		return nil, TxSetPatch{}, errors.New("only one-byte binary archive lengths are supported")
	}
	oldLen := int(plain[lengthOffset])
	oldStart := lengthOffset + 1
	oldEnd := oldStart + oldLen
	patched := make([]byte, 0, len(plain)-oldLen+len(newExtra))
	patched = append(patched, plain[:lengthOffset]...)
	patched = append(patched, byte(len(newExtra)))
	patched = append(patched, newExtra...)
	patched = append(patched, plain[oldEnd:]...)
	return patched, TxSetPatch{
		ExtraLengthOffset: lengthOffset,
		OldExtraLength:    oldLen,
		NewExtraLength:    len(newExtra),
		PayloadHex:        hex.EncodeToString(payload),
		NewExtraHex:       hex.EncodeToString(newExtra),
	}, nil
}

func locateDefaultExtra(plain []byte) (int, []byte, error) {
	oldLen := 33 + len(defaultEncryptedPaymentIDNonce)
	var matches []int
	for off := 0; ; off++ {
		i := bytes.Index(plain[off:], defaultEncryptedPaymentIDNonce)
		if i < 0 {
			break
		}
		i += off
		extraStart := i - 33
		lengthOffset := extraStart - 1
		if extraStart >= 0 && lengthOffset >= 0 &&
			plain[extraStart] == xns.TxExtraPubKey &&
			int(plain[lengthOffset]) == oldLen {
			matches = append(matches, lengthOffset)
		}
		off = i
	}
	if len(matches) == 0 {
		return 0, nil, errors.New("could not find expected default tx_extra")
	}
	if len(matches) > 1 {
		return 0, nil, fmt.Errorf("found %d possible tx_extra fields; refusing", len(matches))
	}
	lengthOffset := matches[0]
	extra := plain[lengthOffset+1 : lengthOffset+1+oldLen]
	return lengthOffset, append([]byte(nil), extra...), nil
}

func chachaKey(secret []byte) []byte {
	var cn cryptonight.State
	hash := cn.Sum(secret, cryptonight.V0, false)
	return hash[:]
}

func fastHash(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

func chacha20XOR(dst, src, key, nonce []byte) {
	if len(key) != 32 || len(nonce) != 8 {
		panic("invalid chacha20 key or nonce length")
	}
	var state [16]uint32
	state[0] = 0x61707865
	state[1] = 0x3320646e
	state[2] = 0x79622d32
	state[3] = 0x6b206574
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[i*4:])
	}
	state[14] = binary.LittleEndian.Uint32(nonce[:4])
	state[15] = binary.LittleEndian.Uint32(nonce[4:])

	var block [64]byte
	for len(src) > 0 {
		chacha20Block(&block, &state)
		n := len(block)
		if len(src) < n {
			n = len(src)
		}
		for i := 0; i < n; i++ {
			dst[i] = src[i] ^ block[i]
		}
		src = src[n:]
		dst = dst[n:]
		state[12]++
		if state[12] == 0 {
			state[13]++
		}
	}
}

func chacha20Block(out *[64]byte, state *[16]uint32) {
	x := *state
	for i := 0; i < 10; i++ {
		quarterRound(&x[0], &x[4], &x[8], &x[12])
		quarterRound(&x[1], &x[5], &x[9], &x[13])
		quarterRound(&x[2], &x[6], &x[10], &x[14])
		quarterRound(&x[3], &x[7], &x[11], &x[15])
		quarterRound(&x[0], &x[5], &x[10], &x[15])
		quarterRound(&x[1], &x[6], &x[11], &x[12])
		quarterRound(&x[2], &x[7], &x[8], &x[13])
		quarterRound(&x[3], &x[4], &x[9], &x[14])
	}
	for i := range x {
		binary.LittleEndian.PutUint32(out[i*4:], x[i]+state[i])
	}
}

func quarterRound(a, b, c, d *uint32) {
	*a += *b
	*d ^= *a
	*d = (*d << 16) | (*d >> 16)
	*c += *d
	*b ^= *c
	*b = (*b << 12) | (*b >> 20)
	*a += *b
	*d ^= *a
	*d = (*d << 8) | (*d >> 24)
	*c += *d
	*b ^= *c
	*b = (*b << 7) | (*b >> 25)
}

func moneroSignature(messageHash, secret []byte) ([]byte, error) {
	if len(messageHash) != 32 {
		return nil, errors.New("message hash must be 32 bytes")
	}
	a, err := scalarFromLE(secret)
	if err != nil {
		return nil, err
	}
	pub := new(edwards25519.Point).ScalarBaseMult(a).Bytes()
	zero := edwards25519.NewScalar()
	for {
		kb := make([]byte, 64)
		if _, err := rand.Read(kb); err != nil {
			return nil, err
		}
		k, err := new(edwards25519.Scalar).SetUniformBytes(kb)
		if err != nil {
			return nil, err
		}
		if k.Equal(zero) == 1 {
			continue
		}
		commit := new(edwards25519.Point).ScalarBaseMult(k).Bytes()
		c, err := scalarFromHash(messageHash, pub, commit)
		if err != nil {
			return nil, err
		}
		if c.Equal(zero) == 1 {
			continue
		}
		ca := new(edwards25519.Scalar).Multiply(c, a)
		r := new(edwards25519.Scalar).Subtract(k, ca)
		if r.Equal(zero) == 1 {
			continue
		}
		out := append(append([]byte(nil), c.Bytes()...), r.Bytes()...)
		return out, nil
	}
}

func scalarFromHash(parts ...[]byte) (*edwards25519.Scalar, error) {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return scalarFromLE(h.Sum(nil))
}

func scalarFromLE(raw []byte) (*edwards25519.Scalar, error) {
	if len(raw) != 32 {
		return nil, errors.New("scalar must be 32 bytes")
	}
	be := append([]byte(nil), raw...)
	for i, j := 0, len(be)-1; i < j; i, j = i+1, j-1 {
		be[i], be[j] = be[j], be[i]
	}
	n := new(big.Int).SetBytes(be)
	n.Mod(n, edwardsOrder)
	out := n.Bytes()
	if len(out) > 32 {
		return nil, errors.New("scalar overflow")
	}
	le := make([]byte, 32)
	for i := range out {
		le[i] = out[len(out)-1-i]
	}
	return new(edwards25519.Scalar).SetCanonicalBytes(le)
}

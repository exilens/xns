package xns

import (
	"encoding/hex"
	"errors"

	"filippo.io/edwards25519"
	p2address "git.gammaspectra.live/P2Pool/consensus/v5/monero/address"
	p2crypto "git.gammaspectra.live/P2Pool/consensus/v5/monero/crypto"
	"git.gammaspectra.live/P2Pool/consensus/v5/monero/crypto/curve25519"
)

func deriveProtocolAddress(prefix uint64) (string, error) {
	spend, err := ProtocolSpendPublicKey()
	if err != nil {
		return "", err
	}
	view, err := ProtocolViewPublicKey()
	if err != nil {
		return "", err
	}
	return string(p2address.FromRawAddress(uint8(prefix), publicKeyBytes(spend), publicKeyBytes(view)).ToBase58()), nil
}

func ProtocolSpendPublicKey() ([]byte, error) {
	return p2crypto.BiasedHashToPoint(new(curve25519.ConstantTimePublicKey), []byte(ProtocolSpendPublicKeyInput)).Bytes(), nil
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

func publicKeyBytes(raw []byte) curve25519.PublicKeyBytes {
	var out curve25519.PublicKeyBytes
	copy(out[:], raw)
	return out
}

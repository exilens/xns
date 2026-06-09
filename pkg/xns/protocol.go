package xns

import (
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	NameSize       = 32
	OwnerKeySize   = 32
	PayloadSize    = NameSize + OwnerKeySize
	AtomicUnits    = uint64(1_000_000_000_000)
	YearAmount     = AtomicUnits / 100
	BlocksPerYear  = uint64(365 * 24 * 60 * 60 / 120)
	TxExtraNonce   = byte(0x02)
	TxExtraPubKey  = byte(0x01)
	TxExtraAddKeys = byte(0x04)
)

type Payload struct {
	Name       string `json:"name"`
	OwnerKey   string `json:"owner_key"`
	PayloadHex string `json:"payload_hex"`
}

func ValidName(name string) error {
	if len(name) < 1 || len(name) > NameSize {
		return fmt.Errorf("name must be 1..%d bytes", NameSize)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isNameChar(c) {
			return errors.New("name may only contain lowercase a-z, 0-9, and -")
		}
	}
	if !isNameEdge(name[0]) {
		return errors.New("name must start with a lowercase letter or digit")
	}
	if !isNameEdge(name[len(name)-1]) {
		return errors.New("name must end with a lowercase letter or digit")
	}
	return nil
}

func EncodeName(name string) ([NameSize]byte, error) {
	var out [NameSize]byte
	if err := ValidName(name); err != nil {
		return out, err
	}
	copy(out[:], name)
	return out, nil
}

func DecodeName(raw []byte) (string, error) {
	if len(raw) != NameSize {
		return "", fmt.Errorf("name field must be %d bytes", NameSize)
	}
	end := NameSize
	for i, b := range raw {
		if b == 0 {
			end = i
			break
		}
	}
	for _, b := range raw[end:] {
		if b != 0 {
			return "", errors.New("name padding contains non-zero bytes")
		}
	}
	name := string(raw[:end])
	if err := ValidName(name); err != nil {
		return "", err
	}
	return name, nil
}

func BuildPayload(name, ownerKeyHex string) ([PayloadSize]byte, error) {
	var out [PayloadSize]byte
	name32, err := EncodeName(name)
	if err != nil {
		return out, err
	}
	owner, err := DecodeOwnerKey(ownerKeyHex)
	if err != nil {
		return out, err
	}
	copy(out[:NameSize], name32[:])
	copy(out[NameSize:], owner[:])
	return out, nil
}

func DecodePayload(raw []byte) (Payload, error) {
	if len(raw) != PayloadSize {
		return Payload{}, fmt.Errorf("payload must be %d bytes", PayloadSize)
	}
	name, err := DecodeName(raw[:NameSize])
	if err != nil {
		return Payload{}, err
	}
	owner, err := DecodeOwnerKey(hex.EncodeToString(raw[NameSize:]))
	if err != nil {
		return Payload{}, err
	}
	return Payload{
		Name:       name,
		OwnerKey:   hex.EncodeToString(owner[:]),
		PayloadHex: hex.EncodeToString(raw),
	}, nil
}

func DecodeOwnerKey(ownerKeyHex string) ([OwnerKeySize]byte, error) {
	var out [OwnerKeySize]byte
	raw, err := hex.DecodeString(ownerKeyHex)
	if err != nil {
		return out, err
	}
	if len(raw) != OwnerKeySize {
		return out, fmt.Errorf("owner key must be %d bytes", OwnerKeySize)
	}
	copy(out[:], raw)
	if allZero(out[:]) {
		return out, errors.New("owner key must not be all zero")
	}
	if err := validOwnerPoint(out[:]); err != nil {
		return out, err
	}
	return out, nil
}

func YearsFromAmount(amount uint64) (uint64, error) {
	if amount < YearAmount {
		return 0, errors.New("amount is below 0.01 XMR")
	}
	if amount%YearAmount != 0 {
		return 0, errors.New("amount is not a whole multiple of 0.01 XMR")
	}
	return amount / YearAmount, nil
}

func isNameChar(c byte) bool {
	return isNameEdge(c) || c == '-'
}

func isNameEdge(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func allZero(raw []byte) bool {
	for _, b := range raw {
		if b != 0 {
			return false
		}
	}
	return true
}

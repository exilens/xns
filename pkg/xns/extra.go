package xns

import "fmt"

func ExtractPayloads(extra []byte) ([]Payload, error) {
	var out []Payload
	for i := 0; i < len(extra); {
		tag := extra[i]
		i++
		switch tag {
		case 0:
			continue
		case TxExtraPubKey:
			i += 32
		case TxExtraNonce:
			if i >= len(extra) {
				return nil, fmt.Errorf("truncated nonce length")
			}
			n := int(extra[i])
			i++
			if i+n > len(extra) {
				return nil, fmt.Errorf("truncated nonce data")
			}
			data := extra[i : i+n]
			i += n
			if n == PayloadSize {
				payload, err := DecodePayload(data)
				if err != nil {
					return nil, err
				}
				out = append(out, payload)
			}
		case TxExtraAddKeys:
			n, next, err := readVarint(extra, i)
			if err != nil {
				return nil, err
			}
			i = next + int(n)*32
		default:
			return nil, fmt.Errorf("unsupported tx_extra tag %d", tag)
		}
		if i > len(extra) {
			return nil, fmt.Errorf("tx_extra field overruns extra length")
		}
	}
	return out, nil
}

func readVarint(raw []byte, i int) (uint64, int, error) {
	var value uint64
	var shift uint
	for i < len(raw) {
		b := raw[i]
		i++
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, i, nil
		}
		shift += 7
		if shift > 63 {
			return 0, i, fmt.Errorf("varint too large")
		}
	}
	return 0, i, fmt.Errorf("truncated varint")
}

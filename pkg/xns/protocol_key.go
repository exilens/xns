package xns

import (
	"errors"
	"math/big"
)

type projectivePoint struct {
	x *big.Int
	y *big.Int
	z *big.Int
}

var (
	moneroFeSqrtM1 = feFromMoneroLimbs([]int64{-32595792, -7943725, 9377950, 3500415, 12389472, -272473, -25146209, -2005654, 326686, 11406482})
	moneroFeMA     = feFromMoneroLimbs([]int64{-486662, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	moneroFeMA2    = feFromMoneroLimbs([]int64{-12721188, -3529, 0, 0, 0, 0, 0, 0, 0, 0})
	moneroFeFFFB1  = feFromMoneroLimbs([]int64{-31702527, -2466483, -26106795, -12203692, -12169197, -321052, 14850977, -10296299, -16929438, -407568})
	moneroFeFFFB2  = feFromMoneroLimbs([]int64{8166131, -6741800, -17040804, 3154616, 21461005, 1466302, -30876704, -6368709, 10503587, -13363080})
	moneroFeFFFB3  = feFromMoneroLimbs([]int64{-13620103, 14639558, 4532995, 7679154, 16815101, -15883539, -22863840, -14813421, 13716513, -6477756})
	moneroFeFFFB4  = feFromMoneroLimbs([]int64{-21786234, -12173074, 21573800, 4524538, -4645904, 16204591, 8012863, -8444712, 3212926, 6885324})
)

func ProtocolSpendPublicKey() ([]byte, error) {
	return moneroHashToEC([]byte(ProtocolSpendPublicKeyInput))
}

// moneroHashToEC is a Go port of the map used by Monero's crypto::hash_to_ec
// and rct::hash_to_p3:
//
//	cn_fast_hash(input)
//	ge_fromfe_frombytes_vartime(hash)
//	ge_mul8(point)
//
// Reference sources, Monero commit 6476ec8f2ce36d2d850cbd117f2ffd074177ab3a:
//   - https://github.com/monero-project/monero/blob/6476ec8f2ce36d2d850cbd117f2ffd074177ab3a/src/crypto/crypto.cpp#L611-L619
//   - https://github.com/monero-project/monero/blob/6476ec8f2ce36d2d850cbd117f2ffd074177ab3a/src/ringct/rctOps.cpp#L658-L665
//   - https://github.com/monero-project/monero/blob/6476ec8f2ce36d2d850cbd117f2ffd074177ab3a/src/crypto/crypto-ops.c#L2309-L2415
//
// This is distinct from Monero's historical Pedersen generator H, which uses
// 8*ge_frombytes_vartime(cn_fast_hash(G)):
//   - https://github.com/monero-project/monero/blob/6476ec8f2ce36d2d850cbd117f2ffd074177ab3a/src/crypto/generators.cpp#L68-L112
//
// Reference vector for input "XNS":
//
//	cn_fast_hash: a1e2fb4987878353860d495e647ef0bd7b1a27f84686ef582baf9257842eb549
//	before ge_mul8: 8d0c26283ab85878a0f640f3e8bdd93bdbd8d522088f4927213fbf5ef9ecac33
//	after ge_mul8: a0cd652c2b2b9ee7664079650b6a738c7ec3551034c7950a4bc2f1ca02adc999
func moneroHashToEC(data []byte) ([]byte, error) {
	p, err := moneroGeFromFeFromBytes(keccak256(data))
	if err != nil {
		return nil, err
	}
	return encodePoint(scalarMult(p, big.NewInt(8))), nil
}

func moneroGeFromFeFromBytes(raw []byte) (point, error) {
	p, err := moneroGeFromFeFromBytesProjective(raw)
	if err != nil {
		return identity, err
	}
	zInv := inv(p.z)
	return point{mul(p.x, zInv), mul(p.y, zInv)}, nil
}

func moneroGeFromFeFromBytesProjective(raw []byte) (projectivePoint, error) {
	if len(raw) != 32 {
		return projectivePoint{}, errors.New("hash-to-EC input must be 32 bytes")
	}

	u := fieldFromLittleEndian(raw)
	v := mul(big.NewInt(2), mul(u, u))
	w := add(v, big.NewInt(1))
	x := mul(w, w)
	y := mul(moneroFeMA2, v)
	x = add(x, y)

	rX := divPowM1(w, x)
	x = mul(mul(rX, rX), x)
	y = sub(w, x)
	z := new(big.Int).Set(moneroFeMA)
	sign := uint(0)

	if y.Sign() != 0 {
		y = add(w, x)
		if y.Sign() != 0 {
			x = mul(x, moneroFeSqrtM1)
			y = sub(w, x)
			if y.Sign() != 0 {
				rX = mul(rX, moneroFeFFFB3)
			} else {
				rX = mul(rX, moneroFeFFFB4)
			}
			sign = 1
		} else {
			rX = mul(rX, moneroFeFFFB1)
		}
	} else {
		rX = mul(rX, moneroFeFFFB2)
	}

	if rX.Bit(0) != sign {
		rX = sub(edP, rX)
	}

	rX = mul(rX, u)
	z = mul(z, v)
	rY := sub(z, w)
	z = add(z, w)
	rX = mul(rX, z)

	return projectivePoint{rX, rY, z}, nil
}

func fieldFromLittleEndian(raw []byte) *big.Int {
	buf := append([]byte(nil), raw...)
	reverse(buf)
	return mod(new(big.Int).SetBytes(buf))
}

func divPowM1(u, v *big.Int) *big.Int {
	exp := new(big.Int).Sub(edP, big.NewInt(5))
	exp.Div(exp, big.NewInt(8))
	v2 := mul(v, v)
	v3 := mul(v2, v)
	v7 := mul(mul(v3, v3), v)
	uv7 := mul(u, v7)
	pow := new(big.Int).Exp(uv7, exp, edP)
	return mul(mul(pow, v3), u)
}

func feFromMoneroLimbs(limbs []int64) *big.Int {
	value := big.NewInt(0)
	for i, limb := range limbs {
		term := big.NewInt(limb)
		term.Lsh(term, uint(25*i+(i+1)/2))
		value.Add(value, term)
	}
	return mod(value)
}

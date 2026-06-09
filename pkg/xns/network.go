package xns

const (
	MainnetAddressPrefix  = uint64(18)
	StagenetAddressPrefix = uint64(24)
	TestnetAddressPrefix  = uint64(53)

	ProtocolViewSecret     = "c2694f7b2ba66ada8548e31c4ef1616ddce71b47969a337c293f7a72d5804909"
	ProtocolSpendPublicKey = "ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f"

	MainnetProtocolRestoreHeight  = uint64(3690551)
	StagenetProtocolRestoreHeight = uint64(2135563)
	TestnetProtocolRestoreHeight  = uint64(3018160)
)

func ProtocolAddress(stagenet bool) string {
	prefix := MainnetAddressPrefix
	if stagenet {
		prefix = StagenetAddressPrefix
	}
	addr, err := deriveProtocolAddress(prefix)
	if err != nil {
		panic(err)
	}
	return addr
}

func TestnetProtocolAddress() string {
	addr, err := deriveProtocolAddress(TestnetAddressPrefix)
	if err != nil {
		panic(err)
	}
	return addr
}

func ProtocolRestoreHeight(stagenet bool) uint64 {
	if stagenet {
		return StagenetProtocolRestoreHeight
	}
	return MainnetProtocolRestoreHeight
}

func NetworkName(stagenet bool) string {
	if stagenet {
		return "stagenet"
	}
	return "mainnet"
}

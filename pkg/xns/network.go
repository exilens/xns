package xns

const (
	MainnetAddressPrefix  = uint64(18)
	StagenetAddressPrefix = uint64(24)
	TestnetAddressPrefix  = uint64(53)

	ProtocolViewSecret          = "935830fc11250e25153160951c1ba9152e5fee00763890314b67532ae6385607"
	ProtocolSpendPublicKeyInput = "XNS"

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

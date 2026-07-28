package login

// VersionToProtocol maps a Minecraft version name to its protocol
// version. Versions come from disconnect messages like "Please use
// version 1.21.11" or "Required version: 1.20.1". The mapping is only
// used to fall back when a server rejects our first handshake with an
// outdated/incompatible message, so an exact match to a few common
// releases is enough.
//
// Sources: https://wiki.vg/Protocol_version_numbers and Mojang release
// notes. Bounds are kept wide enough for snapshot builds.
var VersionToProtocol = map[string]int32{
	// 1.20.x
	"1.20.5": 766,
	"1.20.6": 767,

	// 1.21.x
	"1.21":    769,
	"1.21.1":  770,
	"1.21.2":  768,
	"1.21.3":  768,
	"1.21.4":  769,
	"1.21.5":  770,
	"1.21.6":  771,
	"1.21.7":  772,
	"1.21.8":  773,
	"1.21.9":  774,
	"1.21.10": 775,
	"1.21.11": 776,
	"1.21.12": 777,
	"1.21.13": 778,
	"1.21.14": 779,
	"1.21.15": 780,
	"1.21.16": 781,
}

// ProtocolForVersion looks up the protocol number for a version name
// and returns the legacy (-1) sentinel when the version is unknown.
// Callers pass the result to Handshake as protoVersion.
func ProtocolForVersion(version string) int32 {
	if version == "" {
		return protocolVersionLegacy
	}
	if p, ok := VersionToProtocol[version]; ok {
		return p
	}
	return protocolVersionLegacy
}

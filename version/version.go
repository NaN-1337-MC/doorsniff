package version

// Version is set at build time via -ldflags:
//
//	-X github.com/renanbray/doorsniff/version.Version=$(git describe --tags)
//
// If git has no tags yet, it defaults to "dev".
var Version = "dev"

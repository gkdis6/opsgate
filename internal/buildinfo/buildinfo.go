// Package buildinfo reports the executable version.
package buildinfo

import "runtime/debug"

// version is set at build time with -ldflags.
var version = ""

// Version returns the release version, or dev for local builds.
func Version() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return moduleVersion(info)
}

func moduleVersion(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return "dev"
}

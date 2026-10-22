package toolio

import (
	"runtime"
	"runtime/debug"
)

// VersionInfo is the version document: what --version prints, and what the
// agent-fox service answers on GET /version.
type VersionInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// VersionDocument is the identity of the build as the one document that
// --version prints: the name of the program, and the version, commit and build
// time that the build set or its build information has, with the Go version it
// was built with.
func (a *App) VersionDocument() VersionInfo { return a.version() }

// Identity is the version and the commit of the build, as --version prints them: what the
// build set, else what its build information has. A commit that nothing knows is empty.
func (a *App) Identity() (version, commit string) {
	v := a.version()
	return v.Version, v.Commit
}

// version is the build's identity. A value the build did not set with
// -ldflags comes from the binary's build information (the main module's
// version, vcs.revision and vcs.time); with neither, the version is "dev"
// and the rest are empty.
func (a *App) version() VersionInfo {
	v := VersionInfo{
		Name:      a.name(),
		Version:   a.Version,
		Commit:    a.Commit,
		BuildTime: a.BuildTime,
		GoVersion: runtime.Version(),
	}
	if v.Version == "" || v.Commit == "" || v.BuildTime == "" {
		read := a.BuildInfo
		if read == nil {
			read = debug.ReadBuildInfo
		}
		if info, ok := read(); ok && info != nil {
			if v.Version == "" && info.Main.Version != "(devel)" {
				v.Version = info.Main.Version
			}
			for _, s := range info.Settings {
				switch {
				case s.Key == "vcs.revision" && v.Commit == "":
					v.Commit = s.Value
				case s.Key == "vcs.time" && v.BuildTime == "":
					v.BuildTime = s.Value
				}
			}
		}
	}
	if v.Version == "" {
		v.Version = "dev"
	}
	return v
}

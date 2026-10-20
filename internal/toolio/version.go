package toolio

import (
	"runtime"
	"runtime/debug"
)

// versionInfo is the document --version prints.
type versionInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// version is the build's identity. A value the build did not set with
// -ldflags comes from the binary's build information (the main module's
// version, vcs.revision and vcs.time); with neither, the version is "dev"
// and the rest are empty.
func (a *App) version() versionInfo {
	v := versionInfo{
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

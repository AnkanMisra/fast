package main

import (
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const appName = "fast"

var (
	version   = ""
	commit    = ""
	buildDate = ""
)

type versionKind string

const (
	versionKindDev     versionKind = "dev"
	versionKindRelease versionKind = "release"
	versionKindPseudo  versionKind = "pseudo"
)

type versionInfo struct {
	Kind       versionKind
	Version    string
	Commit     string
	Date       string
	PseudoTime time.Time
}

func currentVersionInfo() versionInfo {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return resolveVersionInfo(nil, version, commit, buildDate)
	}
	return resolveVersionInfo(buildInfo, version, commit, buildDate)
}

func resolveVersionInfo(buildInfo *debug.BuildInfo, overrideVersion, overrideCommit, overrideDate string) versionInfo {
	info := versionInfo{
		Kind:    versionKindDev,
		Version: "dev",
	}

	if buildInfo != nil {
		buildVersion := buildInfo.Main.Version
		switch {
		case module.IsPseudoVersion(buildVersion):
			info.Kind = versionKindPseudo
			info.Version = buildVersion
			if pseudoTime, err := module.PseudoVersionTime(buildVersion); err == nil {
				info.PseudoTime = pseudoTime
			}
		case semver.IsValid(buildVersion):
			info.Kind = versionKindRelease
			info.Version = buildVersion
		case buildVersion != "" && buildVersion != "(devel)":
			info.Version = buildVersion
		}

		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = setting.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = setting.Value
				}
			}
		}
	}

	if overrideVersion != "" {
		info.Kind = versionKindRelease
		info.Version = overrideVersion
		info.PseudoTime = time.Time{}
	}
	if overrideCommit != "" {
		info.Commit = overrideCommit
	}
	if overrideDate != "" {
		info.Date = overrideDate
	}

	return info
}

func (v versionInfo) isOutdated(release latestRelease) bool {
	if !semver.IsValid(release.TagName) {
		return false
	}

	switch v.Kind {
	case versionKindRelease:
		return semver.Compare(v.Version, release.TagName) < 0
	case versionKindPseudo:
		if v.PseudoTime.IsZero() || release.CreatedAt.IsZero() {
			return false
		}
		return release.CreatedAt.After(v.PseudoTime)
	default:
		return false
	}
}

func (v versionInfo) versionString() string {
	if v.Version == "" {
		return "dev"
	}
	return v.Version
}

func (v versionInfo) cliString() string {
	lines := []string{fmt.Sprintf("%s %s", appName, v.versionString())}
	if v.Commit != "" {
		lines = append(lines, "commit: "+shortCommit(v.Commit))
	}
	if v.Date != "" {
		lines = append(lines, "built: "+v.Date)
	}
	return strings.Join(lines, "\n")
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

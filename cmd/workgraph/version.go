package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

var (
	version   string
	commit    string
	buildDate string
)

type buildIdentity struct {
	Version   string
	Commit    string
	BuildDate string
}

func runVersion(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph version")
		return 2
	}

	identity := currentBuildIdentity()
	fmt.Fprintf(stdout, "workgraph %s\ncommit: %s\nbuilt: %s\n", identity.Version, identity.Commit, identity.BuildDate)
	return 0
}

func currentBuildIdentity() buildIdentity {
	identity := buildIdentity{
		Version:   strings.TrimSpace(version),
		Commit:    strings.TrimSpace(commit),
		BuildDate: strings.TrimSpace(buildDate),
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if identity.Version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			identity.Version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if identity.Commit == "" {
					identity.Commit = setting.Value
				}
			case "vcs.time":
				if identity.BuildDate == "" {
					identity.BuildDate = setting.Value
				}
			}
		}
	}

	if identity.Version == "" {
		identity.Version = "dev"
	}
	if identity.Commit == "" {
		identity.Commit = "unknown"
	}
	if identity.BuildDate == "" {
		identity.BuildDate = "unknown"
	}
	return identity
}

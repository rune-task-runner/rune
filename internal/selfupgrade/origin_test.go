package selfupgrade

import "testing"

func TestDetectOrigin(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	none := env()
	tests := []struct {
		name    string
		version string
		stamped bool
		path    string
		goos    string
		getenv  func(string) string
		want    Origin
	}{
		{"dev version", "dev", false, "/usr/local/bin/rune", "linux", none, OriginDevelopment},
		{"unparseable version", "not-a-version", true, "/usr/local/bin/rune", "linux", none, OriginDevelopment},
		{"empty version", "", true, "/usr/local/bin/rune", "linux", none, OriginDevelopment},
		{"dev inside homebrew", "dev", false, "/opt/homebrew/Caskroom/rune/0.7.0/rune", "darwin", none, OriginDevelopment},
		{"go install", "0.7.0", false, "/home/me/go/bin/rune", "linux", none, OriginGoInstall},
		{"go install in container", "0.7.0", false, "/rune", "linux", env("RUNE_CONTAINER", "1"), OriginGoInstall},
		{"container", "0.7.0", true, "/rune", "linux", env("RUNE_CONTAINER", "1"), OriginContainer},
		{"container flag off", "0.7.0", true, "/rune", "linux", env("RUNE_CONTAINER", "0"), OriginStandalone},
		{"homebrew cask, apple silicon", "0.7.0", true, "/opt/homebrew/Caskroom/rune/0.7.0/rune", "darwin", none, OriginHomebrew},
		{"homebrew cellar, intel", "0.7.0", true, "/usr/local/Cellar/rune/0.7.0/bin/rune", "darwin", none, OriginHomebrew},
		{"linuxbrew", "0.7.0", true, "/home/linuxbrew/.linuxbrew/Caskroom/rune/0.7.0/rune", "linux", none, OriginHomebrew},
		{"scoop default", "0.7.0", true, `C:\Users\me\scoop\apps\rune\0.7.0\rune.exe`, "windows", none, OriginScoop},
		{"scoop mixed case", "0.7.0", true, `C:\Users\me\Scoop\Apps\rune\current\rune.exe`, "windows", none, OriginScoop},
		{"scoop custom root", "0.7.0", true, `D:\tools\apps\rune\0.7.0\rune.exe`, "windows", env("SCOOP", `D:\tools`), OriginScoop},
		{"scoop custom root, trailing slash", "0.7.0", true, `D:\tools\apps\rune\0.7.0\rune.exe`, "windows", env("SCOOP", `D:\tools\`), OriginScoop},
		{"scoop global root", "0.7.0", true, `D:\tools\apps\rune\0.7.0\rune.exe`, "windows", env("SCOOP_GLOBAL", `d:\TOOLS`), OriginScoop},
		{"usr local", "0.7.0", true, "/usr/local/bin/rune", "linux", none, OriginStandalone},
		{"home local", "0.7.0", true, "/home/me/.local/bin/rune", "linux", none, OriginStandalone},
		{"lookalike directory", "0.7.0", true, "/home/me/cellar-door/Caskroom-old/rune", "linux", none, OriginStandalone},
		{"lowercase cellar", "0.7.0", true, "/home/me/cellar/rune", "linux", none, OriginStandalone},
		{"windows tools", "0.7.0", true, `C:\tools\rune.exe`, "windows", none, OriginStandalone},
		{"apps without scoop", "0.7.0", true, `D:\tools\apps\rune\rune.exe`, "windows", none, OriginStandalone},
		{"scoop path on linux", "0.7.0", true, "/home/me/scoop/apps/rune", "linux", none, OriginStandalone},
		{"scoop variable on linux", "0.7.0", true, "/opt/tools/apps/rune", "linux", env("SCOOP", "/opt/tools"), OriginStandalone},
		{"cellar directory on windows", "0.7.0", true, `C:\Cellar\rune.exe`, "windows", none, OriginStandalone},
		{"caskroom directory on windows", "0.7.0", true, `C:\Caskroom\rune.exe`, "windows", none, OriginStandalone},
		{"scoop without apps", "0.7.0", true, `C:\scoop\rune.exe`, "windows", none, OriginStandalone},
	}
	for _, tt := range tests {
		if got := detectOrigin(tt.version, tt.stamped, tt.path, tt.goos, tt.getenv); got != tt.want {
			t.Errorf("%s: detectOrigin = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRefusedMessages(t *testing.T) {
	tests := []struct {
		r    Refused
		want string
	}{
		{
			Refused{Origin: OriginHomebrew, Path: "/opt/homebrew/Caskroom/rune/0.7.0/rune"},
			"this rune was installed with Homebrew (/opt/homebrew/Caskroom/rune/0.7.0/rune); run: brew upgrade rune",
		},
		{
			Refused{Origin: OriginScoop, Path: `C:\Users\me\scoop\apps\rune\0.7.0\rune.exe`},
			`this rune was installed with Scoop (C:\Users\me\scoop\apps\rune\0.7.0\rune.exe); run: scoop update rune`,
		},
		{
			Refused{Origin: OriginGoInstall},
			"this rune was built with go install; run: go install github.com/rune-task-runner/rune/cmd/rune@latest",
		},
		{
			Refused{Origin: OriginContainer},
			"this rune runs from the container image; pull a newer one: docker pull ghcr.io/rune-task-runner/rune:latest",
		},
		{
			Refused{Origin: OriginDevelopment, Version: "dev"},
			`this is a development build (version "dev") and is not upgraded; install a release: https://github.com/rune-task-runner/rune/releases/latest`,
		},
	}
	for _, tt := range tests {
		if got := tt.r.Error(); got != tt.want {
			t.Errorf("%s:\n got  %q\n want %q", tt.r.Origin, got, tt.want)
		}
	}
}

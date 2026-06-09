package installer

import (
	"path/filepath"
)

// Layout is the on-disk install layout. Default values match the existing
// manual deployments documented in docs/use-cases/.
type Layout struct {
	InstallDir   string // e.g. /opt/farmhand
	BinaryPath   string // /usr/local/bin/farmhand (where install.sh placed us)
	ConfigPath   string // <InstallDir>/farmhand.yaml
	DatabasePath string // <InstallDir>/farmhand.db
	ArtifactDir  string // <InstallDir>/artifacts
	ResultDir    string // <InstallDir>/results
	LogDir       string // <InstallDir>/logs
}

// DefaultLayout returns the canonical /opt/farmhand layout that both the
// Ubuntu and Mac mini deployments use today, so the installer produces a tree
// shape that's byte-compatible with our existing manual installs.
func DefaultLayout() Layout {
	return DerivedLayout("/opt/farmhand")
}

// DerivedLayout fans a layout out from a given InstallDir. The binary itself
// lives at /usr/local/bin/farmhand (placed there by install.sh); the
// install dir holds everything else (config, database, runtime dirs).
func DerivedLayout(installDir string) Layout {
	return Layout{
		InstallDir:   installDir,
		BinaryPath:   "/usr/local/bin/farmhand",
		ConfigPath:   filepath.Join(installDir, "farmhand.yaml"),
		DatabasePath: filepath.Join(installDir, "farmhand.db"),
		ArtifactDir:  filepath.Join(installDir, "artifacts"),
		ResultDir:    filepath.Join(installDir, "results"),
		LogDir:       filepath.Join(installDir, "logs"),
	}
}

// RuntimeDirs returns the directories the installer must mkdir -p for the
// daemon to function.
func (l Layout) RuntimeDirs() []string {
	return []string{l.InstallDir, l.ArtifactDir, l.ResultDir, l.LogDir}
}

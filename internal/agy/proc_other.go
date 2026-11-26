//go:build !windows

package agy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func hideBackgroundWindow(_ *exec.Cmd) {}

func CountRunningAgyProcesses() int {
	out, err := exec.Command("pgrep", "-x", "agy").Output()
	if err != nil {
		return 0
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	return len(lines)
}

func setOSAutoStart(enabled bool, exePath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.agyswitch.app.plist")
		if !enabled {
			_ = os.Remove(plistPath)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			return err
		}
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.agyswitch.app</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>--minimized</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`, exePath)
		return os.WriteFile(plistPath, []byte(plist), 0o644)
	}

	desktopPath := filepath.Join(home, ".config", "autostart", "agy-switch.desktop")
	if !enabled {
		_ = os.Remove(desktopPath)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(desktopPath), 0o755); err != nil {
		return err
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=AGY Switch\nExec=%q --minimized\nHidden=false\nNoDisplay=false\nX-GNOME-Autostart-enabled=true\n", exePath)
	return os.WriteFile(desktopPath, []byte(entry), 0o644)
}

func getOSAutoStart() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if runtime.GOOS == "darwin" {
		_, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.agyswitch.app.plist"))
		return err == nil
	}
	_, err = os.Stat(filepath.Join(home, ".config", "autostart", "agy-switch.desktop"))
	return err == nil
}

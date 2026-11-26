package main

import (
	"os"
	"path/filepath"

	"agy-switch/internal/tray"
)

func main() {
	out := filepath.Join("build", "appicon.png")
	if _, err := os.Stat(out); err == nil {
		return
	}
	_ = os.MkdirAll("build", 0o755)
	if err := os.WriteFile(out, tray.AppIconPNG(256), 0o644); err != nil {
		panic(err)
	}
}

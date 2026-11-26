package main

import (
	"embed"
	"log"
	"os"
	"strings"

	"agy-switch/internal/creds"
	"agy-switch/internal/ipc"
	"agy-switch/internal/store"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	baseDir, err := store.DefaultBaseDir()
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.New(baseDir)
	if err != nil {
		log.Fatal(err)
	}
	paths, err := creds.DefaultTargetPaths()
	if err != nil {
		log.Fatal(err)
	}
	syncer := creds.NewSyncer(paths, creds.OSKeyringProvider{})

	var appRef *App
	ipcSrv, alreadyRunning, err := ipc.TryStartServer(baseDir, func(cmd string) {
		if appRef == nil {
			return
		}
		switch strings.ToUpper(cmd) {
		case "SHOW":
			appRef.ShowPanel()
		case "RELOAD":
			appRef.notifyChanged()
		case "QUIT":
			appRef.QuitApp()
		}
	})
	if err == nil && alreadyRunning {
		// 已有后台托盘实例在运行，已自动唤醒其主面板，当前进程直接退出
		return
	}

	startHidden := false
	for _, arg := range os.Args[1:] {
		if arg == "--minimized" || arg == "--tray" || arg == "-m" {
			startHidden = true
			break
		}
	}

	appRef = NewApp(st, syncer, ipcSrv)

	err = wails.Run(&options.App{
		Title:             "AGY Switch",
		Width:             420,
		Height:            440,
		MinWidth:          370,
		MinHeight:         360,
		StartHidden:       startHidden,
		HideWindowOnClose: true,
		BackgroundColour:  &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:     appRef.startup,
		OnBeforeClose: appRef.beforeClose,
		OnShutdown:    appRef.shutdown,
		Bind: []interface{}{
			appRef,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

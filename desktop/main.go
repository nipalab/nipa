package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/nipalab/nipa/desktop/internal/connector"
	"github.com/nipalab/nipa/desktop/internal/service"
	"github.com/nipalab/nipa/desktop/internal/workspace"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	storePath, err := workspace.DefaultPath()
	if err != nil {
		log.Fatal(err)
	}
	var app *application.App
	svc := service.New(
		service.ConnectorDialer(connector.Options{LogPath: daemonLogPath()}),
		workspace.NewStore(storePath),
		func() (string, error) {
			return app.Dialog.OpenFile().
				SetTitle("Open a Nipa clone").
				CanChooseDirectories(true).
				CanChooseFiles(false).
				PromptForSingleSelection()
		},
	)

	app = application.New(application.Options{
		Name:        "Nipa",
		Description: "Version control for game teams",
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Nipa",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 560,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(15, 17, 21),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func daemonLogPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "nipa", "daemon.log")
}

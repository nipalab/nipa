package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/nipalab/nipa/desktop/internal/autostart"
	"github.com/nipalab/nipa/desktop/internal/connector"
	"github.com/nipalab/nipa/desktop/internal/service"
	"github.com/nipalab/nipa/desktop/internal/tray"
	"github.com/nipalab/nipa/desktop/internal/workspace"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/trayicon.png
var trayIcon []byte

func init() {
	application.RegisterEvent[service.RepoChanged](service.EventRepoChanged)
	application.RegisterEvent[service.OpProgress](service.EventOpProgress)
	application.RegisterEvent[service.RepoActivity](service.EventRepoActivity)
	application.RegisterEvent[service.Navigation](service.EventNavigate)
}

func main() {
	storePath, err := workspace.DefaultPath()
	if err != nil {
		log.Fatal(err)
	}
	store := workspace.NewStore(storePath)
	var app *application.App
	var window *application.WebviewWindow
	show := func() {
		if window != nil {
			window.Show()
			window.Focus()
		}
	}
	svc := service.New(
		service.ConnectorDialer(connector.Options{LogPath: daemonLogPath()}),
		store,
		func() (string, error) {
			return app.Dialog.OpenFile().
				SetTitle("Open a Nipa clone").
				CanChooseDirectories(true).
				CanChooseFiles(false).
				PromptForSingleSelection()
		},
	)
	svc.Prefs = store
	svc.Events = func(name string, data any) { app.Event.Emit(name, data) }
	svc.PickFileFunc = func(dir string) (string, error) {
		return app.Dialog.OpenFile().
			SetTitle("Choose a file to lock").
			SetDirectory(dir).
			CanChooseFiles(true).
			CanChooseDirectories(false).
			PromptForSingleSelection()
	}
	svc.OpenFunc = func(path string) error { return app.Browser.OpenFile(path) }
	svc.ChooseFolderFunc = func(title string) (string, error) {
		return app.Dialog.OpenFile().
			SetTitle(title).
			CanChooseDirectories(true).
			CanChooseFiles(false).
			CanCreateDirectories(true).
			PromptForSingleSelection()
	}
	svc.ChooseProgramFunc = func() (string, error) {
		return app.Dialog.OpenFile().
			SetTitle("Choose a program").
			CanChooseFiles(true).
			CanChooseDirectories(false).
			PromptForSingleSelection()
	}
	if exe, err := os.Executable(); err == nil {
		svc.Autostart = func(enabled bool) error { return autostart.Set(enabled, exe) }
	}

	notifier := notifications.New()
	svc.Notifier = func(n service.Notification) error {
		return notifier.SendNotification(notifications.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body, Data: n.Data()})
	}
	notifier.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			return
		}
		show()
		svc.Navigate(service.NavigationFromData(result.Response.UserInfo))
	})

	openLink := func(link string) {
		if link == "" {
			return
		}
		show()
		go func() {
			if _, err := svc.OpenLink(context.Background(), link); err != nil {
				log.Printf("open %s: %v", link, err)
			}
		}()
	}

	app = application.New(application.Options{
		Name:        "Nipa",
		Description: "Version control for game teams",
		Services:    []application.Service{application.NewService(svc), application.NewService(notifier)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Linux:       application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
		Windows:     application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.nipalab.nipa.desktop",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				show()
				openLink(service.LinkArg(data.Args))
			},
		},
	})

	prefs, _ := store.Settings()
	background := slices.Contains(os.Args[1:], autostart.Arg) && prefs.KeepInTray
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Nipa",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 560,
		Hidden:    background,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(15, 17, 21),
		URL:              "/",
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if st, err := store.Settings(); err == nil && st.KeepInTray {
			e.Cancel()
			window.Hide()
			return
		}
		app.Quit()
	})

	systray := app.SystemTray.New()
	systray.SetIcon(trayIcon)
	act := func(e tray.Entry) {
		switch e.Action {
		case tray.ActionShow:
			show()
		case tray.ActionNavigate:
			show()
			svc.Navigate(e.Target)
		case tray.ActionQuit:
			app.Quit()
		}
	}
	updateTray := func(list []service.RepoActivity) {
		systray.SetTooltip(tray.Tooltip(list))
		systray.SetMenu(trayMenu(tray.Menu(list), act))
	}
	updateTray(nil)
	svc.ActivityHook = updateTray

	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		openLink(e.Context().URL())
	})
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() { _, _ = notifier.RequestNotificationAuthorization() }()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func trayMenu(entries []tray.Entry, act func(tray.Entry)) *application.Menu {
	menu := application.NewMenu()
	for _, e := range entries {
		if e.Separator {
			menu.AddSeparator()
			continue
		}
		menu.Add(e.Label).OnClick(func(*application.Context) { act(e) })
	}
	return menu
}

func daemonLogPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "nipa", "daemon.log")
}

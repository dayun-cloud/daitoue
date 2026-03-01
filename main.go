package main

import (
	"embed"
	"io/fs"
	"os"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	user32          = syscall.NewLazyDLL("user32.dll")
	procCreateMutex = kernel32.NewProc("CreateMutexW")
	procCloseHandle = kernel32.NewProc("CloseHandle")
	procMessageBox  = user32.NewProc("MessageBoxW")
)

func createMutex(name string) (uintptr, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}

	ret, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))

	if err == syscall.ERROR_ALREADY_EXISTS || err == syscall.ERROR_ACCESS_DENIED {
		return ret, syscall.ERROR_ALREADY_EXISTS // Normalize to ALREADY_EXISTS for caller
	}

	return ret, nil
}

func messageBox(title, text string) {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), 0x40|0x1000)
}

func main() {
	mutexName := "Global\\DaitoueAppMutex"

	handle, err := createMutex(mutexName)

	if err == syscall.ERROR_ALREADY_EXISTS {
		// Try to bring existing window to front?
		// That's hard without knowing HWND, but at least we warn the user.
		messageBox("提示", "呆头鹅已在运行，请检查系统托盘")
		os.Exit(0)
	}
	// 程序退出时释放句柄（虽然系统会自动回收，但显式释放是好习惯）
	defer procCloseHandle.Call(handle)

	// Create an instance of the app structure
	app := NewApp()

	assets, _ := fs.Sub(assets, "frontend")

	// Create application with options
	err = wails.Run(&options.App{
		Title:         "呆头鹅",
		Width:         900,
		Height:        600,
		MinWidth:      900,
		MinHeight:     600,
		StartHidden:   true,
		DisableResize: false,
		Frameless:     true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 102, G: 167, B: 189, A: 255}, // #66a7bd
		OnStartup:        app.startup,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true, // This disables default webview drop behavior (open file)
			CSSDropProperty:    "--wails-drop-target",
			CSSDropValue:       "drop",
		},
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			DisableWindowIcon: false,
			Theme:             windows.SystemDefault,
			ZoomFactor:        1.0,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

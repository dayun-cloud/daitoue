package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	goruntime "runtime"

	"github.com/energye/systray"
	"github.com/gen2brain/malgo"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/wav"
	"github.com/moutend/go-hook/pkg/keyboard"
	"github.com/moutend/go-hook/pkg/types"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const CurrentVersion = "v1.1.6"

//go:embed build/icon.ico
var appIcon []byte

// AudioItem represents an audio file and its settings
type AudioItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Hotkey   string `json:"hotkey"` // e.g., "Ctrl+Shift+A"
	Duration string `json:"duration"`
	Size     string `json:"size"`
}

// Config represents the application configuration
type Config struct {
	AudioList        []*AudioItem `json:"audio_list"`
	CloseAction      string       `json:"close_action"` // "minimize" or "quit"
	DontAskAgain     bool         `json:"dont_ask_again"`
	Volume           float64      `json:"volume"`
	MainDevice       string       `json:"main_device"` // Device ID
	AuxDevice        string       `json:"aux_device"`  // Device ID
	WindowWidth      int          `json:"window_width"`
	WindowHeight     int          `json:"window_height"`
	SidebarCollapsed bool         `json:"sidebar_collapsed"`
}

// AudioDevice represents an audio output device
type AudioDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// App struct
type App struct {
	ctx       context.Context
	Config    Config
	mu        sync.Mutex
	playingID string

	// Audio Backend
	malCtx     *malgo.AllocatedContext
	mainDevice *malgo.Device
	auxDevice  *malgo.Device
	audioMu    sync.Mutex

	// Playback State
	// We use beep.StreamSeeker which will be wrapped in Volume
	mainStreamer beep.Streamer
	auxStreamer  beep.Streamer
	mainVolume   *effects.Volume
	auxVolume    *effects.Volume

	stopHook chan bool
}

// GitHubRelease represents the structure of GitHub Release API response
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		BrowserDownloadURL string `json:"browser_download_url"`
		Name               string `json:"name"`
	} `json:"assets"`
}

// CheckUpdateResult represents the result of update check
type CheckUpdateResult struct {
	HasUpdate     bool   `json:"has_update"`
	LatestVersion string `json:"latest_version"`
	DownloadURL   string `json:"download_url"`
	Error         string `json:"error"`
}

// StartUpdate process
func (a *App) StartUpdate(downloadURL string) {
	go func() {
		// Create temporary file
		tempFile := filepath.Join(os.TempDir(), "daitoue_update.exe")
		out, err := os.Create(tempFile)
		if err != nil {
			runtime.EventsEmit(a.ctx, "update-error", "无法创建临时文件: "+err.Error())
			return
		}
		defer out.Close()

		// Download file
		resp, err := http.Get(downloadURL)
		if err != nil {
			runtime.EventsEmit(a.ctx, "update-error", "下载失败: "+err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			runtime.EventsEmit(a.ctx, "update-error", fmt.Sprintf("下载失败，状态码: %d", resp.StatusCode))
			return
		}

		// Progress tracking
		totalSize := resp.ContentLength
		counter := &WriteCounter{
			Total:   uint64(totalSize),
			Context: a.ctx,
		}

		if _, err = io.Copy(out, io.TeeReader(resp.Body, counter)); err != nil {
			runtime.EventsEmit(a.ctx, "update-error", "写入文件失败: "+err.Error())
			return
		}

		out.Close()

		// Success
		runtime.EventsEmit(a.ctx, "update-complete", "下载完成，即将重启...")

		// Prepare bat script to replace and restart
		// We need to:
		// 1. Wait for current app to close
		// 2. Replace exe
		// 3. Start new exe
		// 4. Delete bat script (self-delete is tricky, maybe just leave it in temp)

		exePath, err := os.Executable()
		if err != nil {
			runtime.EventsEmit(a.ctx, "update-error", "无法获取当前程序路径: "+err.Error())
			return
		}

		batPath := filepath.Join(os.TempDir(), "daitoue_update.bat")
		// Fix: Go string is UTF-8, but CMD expects ANSI (GBK) by default.
		// Setting chcp 65001 inside the script is tricky if the script file itself is not UTF-8 without BOM or if CMD environment is weird.
		// Safer approach: Convert the content to GBK (ANSI) and write it.
		// And remove "chcp 65001".
		// Also remove "> NUL" redirection to be safe.

		batContent := fmt.Sprintf("@echo off\r\n"+
			":loop\r\n"+
			"ping 127.0.0.1 -n 2 > nul\r\n"+
			"del \"%s\" > nul 2>&1\r\n"+
			"if exist \"%s\" goto loop\r\n"+
			"move \"%s\" \"%s\" > nul\r\n"+
			"start \"\" \"%s\"\r\n"+
			"del \"%%~f0\" > nul 2>&1\r\n"+
			"exit",
			exePath, exePath, tempFile, exePath, exePath)

		// Convert UTF-8 string to GBK bytes
		gbkEncoder := simplifiedchinese.GBK.NewEncoder()
		gbkContent, err := gbkEncoder.Bytes([]byte(batContent))
		if err != nil {
			// Fallback to raw bytes if conversion fails (though unlikely)
			gbkContent = []byte(batContent)
		}

		if err := os.WriteFile(batPath, gbkContent, 0644); err != nil {
			runtime.EventsEmit(a.ctx, "update-error", "无法创建更新脚本: "+err.Error())
			return
		}

		// Run bat script hidden
		// Use cmd /C to run the batch file
		// Important: We use exec.Command directly which starts a child process.
		// However, Go's os/exec does NOT automatically pass file handles (like the exe file lock) to child processes
		// unless explicitly requested via ExtraFiles. So the child cmd.exe will NOT hold a lock on the parent exe.
		cmd := exec.Command("cmd.exe", "/C", batPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

		if err := cmd.Start(); err != nil {
			runtime.EventsEmit(a.ctx, "update-error", "无法启动更新脚本: "+err.Error())
			return
		}

		// Wait a bit to ensure script started
		time.Sleep(1000 * time.Millisecond)

		// Quit app
		a.Quit()
	}()
}

// WriteCounter tracks download progress
type WriteCounter struct {
	Total   uint64
	Current uint64
	Context context.Context
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Current += uint64(n)
	percentage := float64(wc.Current) / float64(wc.Total) * 100
	runtime.EventsEmit(wc.Context, "update-progress", percentage)
	return n, nil
}

// CheckForUpdates checks for updates from Gitee
func (a *App) CheckForUpdates() CheckUpdateResult {
	resp, err := http.Get("https://gitee.com/api/v5/repos/dayuncloud/daitoue/releases/latest")
	if err != nil {
		return CheckUpdateResult{Error: "无法连接到服务"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CheckUpdateResult{Error: fmt.Sprintf("错误: %d", resp.StatusCode)}
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return CheckUpdateResult{Error: "错误，无法解析响应"}
	}

	latestVersion := release.TagName

	// Remove 'v' prefix
	current := strings.TrimPrefix(CurrentVersion, "v")
	latest := strings.TrimPrefix(latestVersion, "v")

	if current == latest {
		return CheckUpdateResult{HasUpdate: false, LatestVersion: latestVersion}
	}

	// Compare versions
	hasUpdate := compareVersions(latest, current) > 0

	downloadURL := ""
	for _, asset := range release.Assets {
		if asset.Name == "daitoue.exe" {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	// Fallback to generic download link if asset not found
	if downloadURL == "" {
		downloadURL = "https://gitee.com/dayuncloud/daitoue/releases/download/" + latestVersion + "/daitoue.exe"
	}

	return CheckUpdateResult{
		HasUpdate:     hasUpdate,
		LatestVersion: latestVersion,
		DownloadURL:   downloadURL,
	}
}

// compareVersions returns 1 if v1 > v2, -1 if v1 < v2, 0 if equal
func compareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		num1 := 0
		if i < len(parts1) {
			num1, _ = strconv.Atoi(parts1[i])
		}

		num2 := 0
		if i < len(parts2) {
			num2, _ = strconv.Atoi(parts2[i])
		}

		if num1 > num2 {
			return 1
		}
		if num1 < num2 {
			return -1
		}
	}
	return 0
}

// OpenURL opens a URL in the default browser
func (a *App) OpenURL(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		Config: Config{
			AudioList:    []*AudioItem{},
			CloseAction:  "minimize", // default
			Volume:       100,
			WindowWidth:  900,
			WindowHeight: 600,
		},
		stopHook: make(chan bool),
	}
}

// startup is called at application startup
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadConfig()

	// Adaptive window size - REMOVED per requirements (Fixed initial size 900x600)
	if a.Config.WindowWidth > 0 && a.Config.WindowHeight > 0 {
		runtime.WindowSetSize(ctx, a.Config.WindowWidth, a.Config.WindowHeight)
	}
	runtime.WindowCenter(ctx)
	runtime.WindowShow(ctx)

	// Initialize audio
	a.initAudio()

	// Start hotkey listener
	go a.startHotkeyListener()

	// Start system tray
	go func() {
		goruntime.LockOSThread()
		systray.Run(a.onTrayReady, a.onTrayExit)
	}()
}

func (a *App) onTrayReady() {
	systray.SetIcon(appIcon)
	systray.SetTitle("呆头鹅")
	systray.SetTooltip("呆头鹅")

	systray.SetOnClick(func(menu systray.IMenu) {
		a.Show()
	})
	systray.SetOnRClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})

	mShow := systray.AddMenuItem("显示主界面", "显示应用窗口")
	mShow.Click(func() {
		a.Show()
	})

	mQuit := systray.AddMenuItem("退出", "退出应用")
	mQuit.Click(func() {
		// Save window size before quit
		width, height := runtime.WindowGetSize(a.ctx)
		if width > 0 && height > 0 {
			a.SaveWindowSize(width, height)
		}
		a.Quit()
	})

	// Keep the tray running until stopHook
	go func() {
		<-a.stopHook
		systray.Quit()
	}()
}

func (a *App) onTrayExit() {
	// Clean up if needed
}

func (a *App) shutdown(ctx context.Context) {
	a.stopHook <- true
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	return false
}

// startHotkeyListener listens for global key events
func (a *App) startHotkeyListener() {
	keyboardChan := make(chan types.KeyboardEvent, 100)

	if err := keyboard.Install(nil, keyboardChan); err != nil {
		runtime.LogErrorf(a.ctx, "Failed to install keyboard hook: %v", err)
		return
	}
	defer keyboard.Uninstall()

	var pressedKeys = make(map[uint16]bool)

	for {
		select {
		case <-a.stopHook:
			return
		case k := <-keyboardChan:
			if k.Message == types.WM_KEYDOWN || k.Message == types.WM_SYSKEYDOWN {
				pressedKeys[uint16(k.VKCode)] = true
				a.checkHotkeys(pressedKeys)
			} else if k.Message == types.WM_KEYUP || k.Message == types.WM_SYSKEYUP {
				delete(pressedKeys, uint16(k.VKCode))
			}
		}
	}
}

// checkHotkeys checks if currently pressed keys match any audio hotkey
func (a *App) checkHotkeys(pressedKeys map[uint16]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, item := range a.Config.AudioList {
		if item.Hotkey == "" {
			continue
		}
		if a.isHotkeyPressedV2(item.Hotkey, pressedKeys) {
			go a.toggleAudio(item.ID)
			return // Trigger one at a time
		}
	}
}

// keyNameToVKCode maps string names to Windows VK codes
func keyNameToVKCode(name string) uint16 {
	name = strings.ToUpper(name)

	// Single char A-Z, 0-9
	if len(name) == 1 {
		ch := name[0]
		if ch >= 'A' && ch <= 'Z' {
			return uint16(ch)
		}
		if ch >= '0' && ch <= '9' {
			return uint16(ch)
		}
	}

	// Function keys
	if strings.HasPrefix(name, "F") {
		var num int
		fmt.Sscanf(name, "F%d", &num)
		if num >= 1 && num <= 12 {
			return uint16(111 + num) // F1 is 112 (0x70)
		}
	}

	switch name {
	case "CTRL", "CONTROL":
		return 17 // VK_CONTROL
	case "SHIFT":
		return 16 // VK_SHIFT
	case "ALT":
		return 18 // VK_MENU
	case "META", "WIN", "CMD":
		return 91 // VK_LWIN
	case "SPACE":
		return 32
	case "ENTER":
		return 13
	case "ESC", "ESCAPE":
		return 27
	case "TAB":
		return 9
	case "BACKSPACE":
		return 8
	}

	return 0
}

func (a *App) isHotkeyPressedV2(hotkey string, pressedKeys map[uint16]bool) bool {
	keys := strings.Split(hotkey, "+")
	for _, key := range keys {
		kc := keyNameToVKCode(strings.TrimSpace(key))
		if kc == 0 {
			return false
		}

		// Check strictly
		if !pressedKeys[kc] {
			if kc == 17 && (pressedKeys[162] || pressedKeys[163]) {
				continue
			}
			if kc == 16 && (pressedKeys[160] || pressedKeys[161]) {
				continue
			}
			if kc == 18 && (pressedKeys[164] || pressedKeys[165]) {
				continue
			}

			return false
		}
	}
	return true
}

func (a *App) toggleAudio(id string) {
	a.mu.Lock()
	if a.playingID == id {
		a.mu.Unlock()
		a.stopAudio()
		return
	}
	a.mu.Unlock()
	a.playAudio(id)
}

func (a *App) stopAudio() {
	a.audioMu.Lock()
	a.mainStreamer = nil
	a.auxStreamer = nil
	a.mainVolume = nil
	a.auxVolume = nil
	a.audioMu.Unlock()

	a.mu.Lock()
	a.playingID = ""
	a.mu.Unlock()
}

func (a *App) playAudio(id string) {
	// Find item
	a.mu.Lock()
	var item *AudioItem
	for _, i := range a.Config.AudioList {
		if i.ID == id {
			item = i
			break
		}
	}
	vol := a.Config.Volume
	a.mu.Unlock()

	if item == nil {
		return
	}

	// Stop current
	a.stopAudio()

	f, err := os.Open(item.Path)
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to open file: %v", err)
		return
	}
	defer f.Close()

	var streamer beep.StreamSeekCloser
	var format beep.Format

	ext := strings.ToLower(filepath.Ext(item.Path))
	if ext == ".mp3" {
		streamer, format, err = mp3.Decode(f)
	} else if ext == ".wav" {
		streamer, format, err = wav.Decode(f)
	} else {
		runtime.LogErrorf(a.ctx, "Unsupported format: %s", ext)
		return
	}

	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to decode: %v", err)
		return
	}

	// Buffer needed for multiple streams
	buffer := beep.NewBuffer(format)
	buffer.Append(streamer)
	streamer.Close()

	// Create streamers from buffer
	s1 := buffer.Streamer(0, buffer.Len())
	s2 := buffer.Streamer(0, buffer.Len())

	var finalS1, finalS2 beep.Streamer

	// Resample if needed
	if format.SampleRate != 44100 {
		finalS1 = beep.Resample(4, format.SampleRate, 44100, s1)
		finalS2 = beep.Resample(4, format.SampleRate, 44100, s2)
	} else {
		finalS1 = s1
		finalS2 = s2
	}

	// Volume
	v1 := &effects.Volume{Streamer: finalS1, Base: 2, Volume: a.calculateVolume(vol), Silent: vol == 0}
	v2 := &effects.Volume{Streamer: finalS2, Base: 2, Volume: a.calculateVolume(vol), Silent: vol == 0}

	a.audioMu.Lock()
	a.mainStreamer = v1
	a.auxStreamer = v2
	a.mainVolume = v1
	a.auxVolume = v2
	a.audioMu.Unlock()

	a.mu.Lock()
	a.playingID = id
	a.mu.Unlock()
}

// initAudio initializes malgo context
func (a *App) initAudio() {
	var err error
	a.malCtx, err = malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		// fmt.Printf("MALGO: %v\n", message)
	})
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to init malgo context: %v", err)
		return
	}
	a.restartAudioDevices()
}

func (a *App) restartAudioDevices() {
	// 1. Capture old devices and stop playback
	a.audioMu.Lock()
	oldMain := a.mainDevice
	oldAux := a.auxDevice
	a.mainDevice = nil
	a.auxDevice = nil

	// Reset streamers to avoid playing old buffer on new device
	a.mainStreamer = nil
	a.auxStreamer = nil
	a.mainVolume = nil
	a.auxVolume = nil
	a.audioMu.Unlock()

	// 2. Stop old devices (safe to do outside lock)
	if oldMain != nil {
		oldMain.Uninit()
	}
	if oldAux != nil {
		oldAux.Uninit()
	}

	// 3. Prepare Config
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatF32
	deviceConfig.Playback.Channels = 2
	deviceConfig.SampleRate = 44100
	deviceConfig.Alsa.NoMMap = 1

	// 4. Init Main Device
	// IMPORTANT: We need a fresh config copy for each InitDevice call if we modify it
	mainConfig := deviceConfig
	mainID := a.findDeviceID(a.Config.MainDevice)

	if mainID != nil {
		mainConfig.Playback.DeviceID = mainID.Pointer()
	} else {
		// If configured device not found, fallback to default?
		// Or if Config.MainDevice is "default", use nil.
		// If Config.MainDevice is some ID but not found (e.g. unplugged), we should probably fallback to default.
		if a.Config.MainDevice != "" && a.Config.MainDevice != "default" {
			runtime.LogErrorf(a.ctx, "Main device %s not found, falling back to default", a.Config.MainDevice)
		}
		mainConfig.Playback.DeviceID = nil
	}

	onRecvMain := func(pOutput, pInput []byte, framecount uint32) {
		a.onSamples(pOutput, pInput, framecount, false)
	}

	var err error
	var newMain *malgo.Device
	newMain, err = malgo.InitDevice(a.malCtx.Context, mainConfig, malgo.DeviceCallbacks{
		Data: onRecvMain,
	})

	if err == nil {
		err = newMain.Start()
		if err != nil {
			runtime.LogErrorf(a.ctx, "Failed to start main device: %v", err)
		}
	} else {
		runtime.LogErrorf(a.ctx, "Failed to init main device: %v", err)
	}

	// 5. Init Aux Device
	var newAux *malgo.Device
	if a.Config.AuxDevice != "" && a.Config.AuxDevice != "none" {
		auxID := a.findDeviceID(a.Config.AuxDevice)
		if auxID != nil {
			auxConfig := deviceConfig
			auxConfig.Playback.DeviceID = auxID.Pointer()

			onRecvAux := func(pOutput, pInput []byte, framecount uint32) {
				a.onSamples(pOutput, pInput, framecount, true)
			}

			newAux, err = malgo.InitDevice(a.malCtx.Context, auxConfig, malgo.DeviceCallbacks{
				Data: onRecvAux,
			})
			if err == nil {
				err = newAux.Start()
				if err != nil {
					runtime.LogErrorf(a.ctx, "Failed to start aux device: %v", err)
				}
			} else {
				runtime.LogErrorf(a.ctx, "Failed to init aux device: %v", err)
			}
		}
	}

	// 6. Update references
	a.audioMu.Lock()
	a.mainDevice = newMain
	a.auxDevice = newAux
	a.audioMu.Unlock()
}

func (a *App) findDeviceID(idStr string) *malgo.DeviceID {
	if idStr == "" || idStr == "default" || idStr == "none" {
		return nil
	}
	infos, err := a.malCtx.Devices(malgo.Playback)
	if err != nil {
		return nil
	}
	for i := range infos {
		if infos[i].ID.String() == idStr {
			return &infos[i].ID
		}
	}
	return nil
}

func (a *App) onSamples(pOutput, pInput []byte, framecount uint32, isAux bool) {
	a.audioMu.Lock()
	var s beep.Streamer
	if isAux {
		s = a.auxStreamer
	} else {
		s = a.mainStreamer
	}

	if s == nil {
		a.audioMu.Unlock()
		// Zero buffer
		for i := range pOutput {
			pOutput[i] = 0
		}
		return
	}

	// Prepare buffer
	mixSamples := make([][2]float64, framecount)
	n, ok := s.Stream(mixSamples)

	a.audioMu.Unlock() // Unlock after reading

	// Process samples
	for i := 0; i < n; i++ {
		// Left
		sample := float32(mixSamples[i][0])
		u := math.Float32bits(sample)
		pOutput[i*8+0] = byte(u)
		pOutput[i*8+1] = byte(u >> 8)
		pOutput[i*8+2] = byte(u >> 16)
		pOutput[i*8+3] = byte(u >> 24)

		// Right
		sample = float32(mixSamples[i][1])
		u = math.Float32bits(sample)
		pOutput[i*8+4] = byte(u)
		pOutput[i*8+5] = byte(u >> 8)
		pOutput[i*8+6] = byte(u >> 16)
		pOutput[i*8+7] = byte(u >> 24)
	}

	// Zero the rest
	for i := n * 8; i < len(pOutput); i++ {
		pOutput[i] = 0
	}

	if !ok || n < len(mixSamples) {
		a.audioMu.Lock()
		if isAux {
			a.auxStreamer = nil
			a.auxVolume = nil
		} else {
			a.mainStreamer = nil
			a.mainVolume = nil
			if a.playingID != "" {
				// Only clear if main finished?
				// Actually UI doesn't track playing state much except notification.
			}
		}
		a.audioMu.Unlock()

		if !isAux {
			a.mu.Lock()
			a.playingID = ""
			a.mu.Unlock()
		}
	}
}

// Frontend Methods

func (a *App) ImportAudioFile() string {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Audio Files",
		Filters: []runtime.FileFilter{
			{DisplayName: "Audio Files", Pattern: "*.mp3;*.wav"},
		},
	})
	if err != nil || len(paths) == 0 {
		return ""
	}
	return a.ImportAudioFiles(paths)
}

func (a *App) ImportAudioFiles(paths []string) string {
	var results []string
	var addedCount int

	for _, path := range paths {
		// Check limits
		info, err := os.Stat(path)
		if err != nil {
			results = append(results, fmt.Sprintf("Error (%s): %s", filepath.Base(path), err.Error()))
			continue
		}
		if info.Size() > 10*1024*1024 { // 10MB
			results = append(results, fmt.Sprintf("Error (%s): 文件过大 (>10MB)", filepath.Base(path)))
			continue
		}

		// Check duration
		f, err := os.Open(path)
		if err != nil {
			results = append(results, fmt.Sprintf("Error (%s): 无法打开文件", filepath.Base(path)))
			continue
		}

		var duration time.Duration
		var decodeErr error
		ext := strings.ToLower(filepath.Ext(path))

		if ext == ".mp3" {
			streamer, format, err := mp3.Decode(f)
			if err == nil {
				duration = format.SampleRate.D(streamer.Len())
				streamer.Close()
			} else {
				decodeErr = err
			}
		} else if ext == ".wav" {
			streamer, format, err := wav.Decode(f)
			if err == nil {
				duration = format.SampleRate.D(streamer.Len())
				streamer.Close()
			} else {
				decodeErr = err
			}
		}
		f.Close()

		if decodeErr != nil {
			results = append(results, fmt.Sprintf("Error (%s): 解码失败", filepath.Base(path)))
			continue
		}

		if duration.Seconds() > 100 {
			results = append(results, fmt.Sprintf("Error (%s): 时长过长 (>100s)", filepath.Base(path)))
			continue
		}

		// Add to list
		item := &AudioItem{
			ID:       fmt.Sprintf("%d_%d", time.Now().UnixNano(), len(a.Config.AudioList)),
			Name:     filepath.Base(path),
			Path:     path,
			Duration: fmt.Sprintf("%.1fs", duration.Seconds()),
			Size:     fmt.Sprintf("%.2fMB", float64(info.Size())/1024/1024),
		}

		a.mu.Lock()
		a.Config.AudioList = append(a.Config.AudioList, item)
		a.mu.Unlock()
		addedCount++
	}

	if addedCount > 0 {
		a.mu.Lock()
		a.saveConfig()
		a.mu.Unlock()
	}

	if len(results) > 0 {
		return strings.Join(results, "\n")
	}
	return "OK"
}

func (a *App) GetAudios() []AudioItem {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]AudioItem, len(a.Config.AudioList))
	for i, v := range a.Config.AudioList {
		items[i] = *v
	}
	return items
}

func (a *App) DeleteAudio(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, item := range a.Config.AudioList {
		if item.ID == id {
			a.Config.AudioList = append(a.Config.AudioList[:i], a.Config.AudioList[i+1:]...)
			break
		}
	}
	a.saveConfig()
}

func (a *App) UpdateAudioOrder(ids []string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	newOrder := make([]*AudioItem, 0, len(a.Config.AudioList))
	idMap := make(map[string]*AudioItem)

	for _, item := range a.Config.AudioList {
		idMap[item.ID] = item
	}

	for _, id := range ids {
		if item, ok := idMap[id]; ok {
			newOrder = append(newOrder, item)
			delete(idMap, id)
		}
	}

	// Append any remaining items that were not in the ids list (just in case)
	// This handles cases where the frontend might send an incomplete list
	// although it shouldn't happen in normal operation.
	// We iterate original list to maintain relative order of remaining items.
	for _, item := range a.Config.AudioList {
		if _, ok := idMap[item.ID]; ok {
			newOrder = append(newOrder, item)
		}
	}

	a.Config.AudioList = newOrder
	a.saveConfig()
}

func (a *App) UpdateHotkey(id string, hotkey string) {
	// 如果按下的是esc则移除热键
	if strings.EqualFold(hotkey, "esc") || strings.EqualFold(hotkey, "escape") {
		hotkey = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, item := range a.Config.AudioList {
		if item.ID == id {
			item.Hotkey = hotkey
			break
		}
	}
	a.saveConfig() // Ensure saveConfig is called
}

func (a *App) PlayAudioID(id string) {
	a.toggleAudio(id)
}

// Window Control Methods

func (a *App) Minimise() {
	runtime.WindowMinimise(a.ctx)
}

func (a *App) ToggleMaximise() {
	if runtime.WindowIsMaximised(a.ctx) {
		runtime.WindowUnmaximise(a.ctx)
	} else {
		runtime.WindowMaximise(a.ctx)
	}
}

func (a *App) Hide() {
	runtime.WindowHide(a.ctx)
}

func (a *App) Quit() {
	runtime.Quit(a.ctx)
}

func (a *App) Show() {
	runtime.WindowShow(a.ctx)
	// Try to restore if minimized
	runtime.WindowUnminimise(a.ctx)

	// 使用 goroutine 短暂置顶以确保窗口在最前，然后取消置顶
	// 避免同步调用可能导致的竞态问题（窗口卡在置顶状态）
	go func() {
		runtime.WindowSetAlwaysOnTop(a.ctx, true)
		time.Sleep(100 * time.Millisecond)
		runtime.WindowSetAlwaysOnTop(a.ctx, false)
	}()
}

// Config Methods

func (a *App) GetConfig() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Config
}

func (a *App) SaveSettings(closeAction string, dontAskAgain bool) {
	a.mu.Lock()
	a.Config.CloseAction = closeAction
	a.Config.DontAskAgain = dontAskAgain
	a.saveConfig()
	a.mu.Unlock()
}

func (a *App) SaveSidebarState(collapsed bool) {
	a.mu.Lock()
	a.Config.SidebarCollapsed = collapsed
	a.saveConfig()
	a.mu.Unlock()
}

func (a *App) SaveWindowSize(width, height int) {
	a.mu.Lock()
	if width > 0 && height > 0 {
		a.Config.WindowWidth = width
		a.Config.WindowHeight = height
		a.saveConfig()
	}
	a.mu.Unlock()
}

func (a *App) GetAudioDevices() []AudioDevice {
	if a.malCtx == nil {
		return []AudioDevice{}
	}

	infos, err := a.malCtx.Devices(malgo.Playback)
	if err != nil {
		return []AudioDevice{}
	}

	var devices []AudioDevice
	for _, info := range infos {
		devices = append(devices, AudioDevice{
			ID:   info.ID.String(),
			Name: info.Name(),
		})
	}
	return devices
}

func (a *App) SetAudioSettings(mainID, auxID string, volume float64) {
	a.mu.Lock()
	changed := (mainID != a.Config.MainDevice) || (auxID != a.Config.AuxDevice)
	a.Config.MainDevice = mainID
	a.Config.AuxDevice = auxID
	a.Config.Volume = volume
	a.saveConfig()
	a.mu.Unlock()

	if changed {
		a.restartAudioDevices()
	} else {
		// Just update volume
		a.audioMu.Lock()
		if a.mainVolume != nil {
			a.mainVolume.Volume = a.calculateVolume(volume)
			a.mainVolume.Silent = (volume == 0)
		}
		if a.auxVolume != nil {
			a.auxVolume.Volume = a.calculateVolume(volume)
			a.auxVolume.Silent = (volume == 0)
		}
		a.audioMu.Unlock()
	}
}

// ResetAudio completely re-initializes the audio context and devices
func (a *App) ResetAudio() {
	// 1. Stop Playback
	a.stopAudio()

	// 2. Reset Config to defaults
	a.mu.Lock()
	a.Config.MainDevice = "" // Default
	a.Config.AuxDevice = ""  // None
	a.saveConfig()
	a.mu.Unlock()

	// 3. Stop Devices safely (copied from restartAudioDevices logic)
	a.audioMu.Lock()
	oldMain := a.mainDevice
	oldAux := a.auxDevice
	a.mainDevice = nil
	a.auxDevice = nil
	// Reset streamers
	a.mainStreamer = nil
	a.auxStreamer = nil
	a.mainVolume = nil
	a.auxVolume = nil

	// Capture Context to free
	oldCtx := a.malCtx
	a.malCtx = nil
	a.audioMu.Unlock()

	if oldMain != nil {
		oldMain.Uninit()
	}
	if oldAux != nil {
		oldAux.Uninit()
	}
	if oldCtx != nil {
		oldCtx.Free()
	}

	// 4. Re-init
	a.initAudio()
}

func (a *App) calculateVolume(percent float64) float64 {
	if percent <= 0 {
		return -100 // Silent
	}
	if percent >= 100 {
		return 0 // Max volume (0 dB gain relative to source)
	}
	// Simple log scale mapping
	// percent 100 -> 0
	// percent 1 -> -6 (approx, 2^-6 = 1/64)
	// Actually base 2 means +1 volume = x2 amplitude.
	// We want linear slider to feel right.
	// Volume = log2(percent/100)
	return math.Log2(percent / 100.0)
}

func (a *App) getConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "daitoue.json" // fallback
	}
	dir := filepath.Join(configDir, "daitoue")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.MkdirAll(dir, 0755)
	}
	return filepath.Join(dir, "config.json")
}

func (a *App) loadConfig() {
	path := a.getConfigPath()
	b, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(b, &a.Config)
	} else {
		// Try legacy path
		b, err := os.ReadFile("daitoue.json")
		if err == nil {
			// Migrate: old format was list of AudioItem
			var list []*AudioItem
			if json.Unmarshal(b, &list) == nil {
				a.Config.AudioList = list
				a.saveConfig() // Save to new location
			}
		}
	}
}

func (a *App) saveConfig() {
	path := a.getConfigPath()
	b, _ := json.MarshalIndent(a.Config, "", "  ") // Pretty print
	os.WriteFile(path, b, 0644)
}

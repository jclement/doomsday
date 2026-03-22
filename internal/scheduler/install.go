package scheduler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"
)

// systemd unit template for a user-level timer + service.
// Installed to ~/.config/systemd/user/doomsday.{timer,service}.
var systemdTimerTemplate = template.Must(template.New("timer").Parse(`[Unit]
Description=Doomsday Backup Timer

[Timer]
OnBootSec=5min
OnUnitActiveSec=15min
Persistent=true

[Install]
WantedBy=timers.target
`))

var systemdServiceTemplate = template.Must(template.New("service").Parse(`[Unit]
Description=Doomsday Scheduled Backup

[Service]
Type=oneshot
ExecStart={{.BinaryPath}} cron
Environment="HOME={{.Home}}"
`))

// launchd plist template for a user-level launch agent.
// Installed to ~/Library/LaunchAgents/com.doomsday.cron.plist.
var launchdPlistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.doomsday.cron</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>cron</string>
    </array>
    <key>StartInterval</key>
    <integer>900</integer>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
</dict>
</plist>
`))

// installData holds the template parameters for service file generation.
type installData struct {
	BinaryPath string
	Home       string
	LogPath    string
}

// newInstallData resolves the current binary path and home directory.
func newInstallData() (*installData, error) {
	binPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("scheduler.newInstallData: %w", err)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return nil, fmt.Errorf("scheduler.newInstallData: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("scheduler.newInstallData: %w", err)
	}

	logDir := filepath.Join(home, ".local", "state", "doomsday")
	logPath := filepath.Join(logDir, "cron.log")

	return &installData{
		BinaryPath: binPath,
		Home:       home,
		LogPath:    logPath,
	}, nil
}

// SystemdTimerPath returns the path for the user-level systemd timer unit.
func SystemdTimerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("scheduler.SystemdTimerPath: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", "doomsday.timer"), nil
}

// SystemdServicePath returns the path for the user-level systemd service unit.
func SystemdServicePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("scheduler.SystemdServicePath: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", "doomsday.service"), nil
}

// LaunchdPlistPath returns the path for the user-level launchd plist.
func LaunchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("scheduler.LaunchdPlistPath: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.doomsday.cron.plist"), nil
}

// InstallSystemd generates and installs a user-level systemd timer + service.
// After writing the unit files, it runs `systemctl --user enable --now doomsday.timer`.
func InstallSystemd() error {
	data, err := newInstallData()
	if err != nil {
		return err
	}

	timerPath, err := SystemdTimerPath()
	if err != nil {
		return err
	}
	servicePath, err := SystemdServicePath()
	if err != nil {
		return err
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(timerPath), 0700); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: %w", err)
	}

	// Write timer unit.
	timerBuf := &bytes.Buffer{}
	if err := systemdTimerTemplate.Execute(timerBuf, data); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: %w", err)
	}
	if err := os.WriteFile(timerPath, timerBuf.Bytes(), 0644); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: %w", err)
	}

	// Write service unit.
	serviceBuf := &bytes.Buffer{}
	if err := systemdServiceTemplate.Execute(serviceBuf, data); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: %w", err)
	}
	if err := os.WriteFile(servicePath, serviceBuf.Bytes(), 0644); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: %w", err)
	}

	// Enable and start the timer.
	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: daemon-reload: %s: %w", string(out), err)
	}

	cmd = exec.Command("systemctl", "--user", "enable", "--now", "doomsday.timer")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scheduler.InstallSystemd: enable timer: %s: %w", string(out), err)
	}

	return nil
}

// InstallLaunchd generates and installs a launchd plist for the current user.
// After writing the plist, it loads it via `launchctl load`.
func InstallLaunchd() error {
	data, err := newInstallData()
	if err != nil {
		return err
	}

	plistPath, err := LaunchdPlistPath()
	if err != nil {
		return err
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(plistPath), 0700); err != nil {
		return fmt.Errorf("scheduler.InstallLaunchd: %w", err)
	}

	// Ensure log directory exists.
	if err := os.MkdirAll(filepath.Dir(data.LogPath), 0700); err != nil {
		return fmt.Errorf("scheduler.InstallLaunchd: %w", err)
	}

	// Write plist.
	buf := &bytes.Buffer{}
	if err := launchdPlistTemplate.Execute(buf, data); err != nil {
		return fmt.Errorf("scheduler.InstallLaunchd: %w", err)
	}
	if err := os.WriteFile(plistPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("scheduler.InstallLaunchd: %w", err)
	}

	// Load the plist.
	cmd := exec.Command("launchctl", "load", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scheduler.InstallLaunchd: launchctl load: %s: %w", string(out), err)
	}

	return nil
}

// Install auto-detects the platform and installs the appropriate scheduler.
// Returns a human-readable message describing what was installed or, for
// unsupported platforms, manual instructions.
func Install() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if err := InstallSystemd(); err != nil {
			return "", err
		}
		timerPath, _ := SystemdTimerPath()
		servicePath, _ := SystemdServicePath()
		return fmt.Sprintf("Installed systemd timer:\n  %s\n  %s\nEnabled and started doomsday.timer", timerPath, servicePath), nil

	case "darwin":
		if err := InstallLaunchd(); err != nil {
			return "", err
		}
		plistPath, _ := LaunchdPlistPath()
		return fmt.Sprintf("Installed launchd plist:\n  %s\nLoaded com.doomsday.cron", plistPath), nil

	default:
		binPath, _ := os.Executable()
		return fmt.Sprintf("Automatic installation not supported on %s.\nAdd this to your crontab:\n  */15 * * * * %s cron", runtime.GOOS, binPath), nil
	}
}

// Uninstall removes any installed scheduler files for the current platform.
func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		return uninstallSystemd()
	case "darwin":
		return uninstallLaunchd()
	default:
		return fmt.Errorf("scheduler.Uninstall: automatic uninstall not supported on %s", runtime.GOOS)
	}
}

// uninstallSystemd stops and removes the user-level systemd timer + service.
func uninstallSystemd() error {
	// Disable the timer (ignore errors if not active).
	cmd := exec.Command("systemctl", "--user", "disable", "--now", "doomsday.timer")
	_ = cmd.Run()

	timerPath, err := SystemdTimerPath()
	if err != nil {
		return err
	}
	servicePath, err := SystemdServicePath()
	if err != nil {
		return err
	}

	// Remove unit files (ignore errors if they don't exist).
	os.Remove(timerPath)
	os.Remove(servicePath)

	// Reload daemon.
	cmd = exec.Command("systemctl", "--user", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scheduler.uninstallSystemd: daemon-reload: %s: %w", string(out), err)
	}

	return nil
}

// uninstallLaunchd unloads and removes the launchd plist.
func uninstallLaunchd() error {
	plistPath, err := LaunchdPlistPath()
	if err != nil {
		return err
	}

	// Unload (ignore errors if not loaded).
	cmd := exec.Command("launchctl", "unload", plistPath)
	_ = cmd.Run()

	// Remove the plist file.
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scheduler.uninstallLaunchd: %w", err)
	}

	return nil
}

// RenderSystemdTimer returns the rendered systemd timer unit content.
// Useful for preview/dry-run without actually installing.
func RenderSystemdTimer() (string, error) {
	data, err := newInstallData()
	if err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	if err := systemdTimerTemplate.Execute(buf, data); err != nil {
		return "", fmt.Errorf("scheduler.RenderSystemdTimer: %w", err)
	}
	return buf.String(), nil
}

// RenderSystemdService returns the rendered systemd service unit content.
func RenderSystemdService() (string, error) {
	data, err := newInstallData()
	if err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	if err := systemdServiceTemplate.Execute(buf, data); err != nil {
		return "", fmt.Errorf("scheduler.RenderSystemdService: %w", err)
	}
	return buf.String(), nil
}

// RenderLaunchdPlist returns the rendered launchd plist content.
func RenderLaunchdPlist() (string, error) {
	data, err := newInstallData()
	if err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	if err := launchdPlistTemplate.Execute(buf, data); err != nil {
		return "", fmt.Errorf("scheduler.RenderLaunchdPlist: %w", err)
	}
	return buf.String(), nil
}

// RenderSystemdTimerFrom renders the systemd timer template with explicit data.
// This is useful in tests where os.Executable() may not be meaningful.
func RenderSystemdTimerFrom(data installData) (string, error) {
	buf := &bytes.Buffer{}
	if err := systemdTimerTemplate.Execute(buf, &data); err != nil {
		return "", fmt.Errorf("scheduler.RenderSystemdTimerFrom: %w", err)
	}
	return buf.String(), nil
}

// RenderSystemdServiceFrom renders the systemd service template with explicit data.
func RenderSystemdServiceFrom(data installData) (string, error) {
	buf := &bytes.Buffer{}
	if err := systemdServiceTemplate.Execute(buf, &data); err != nil {
		return "", fmt.Errorf("scheduler.RenderSystemdServiceFrom: %w", err)
	}
	return buf.String(), nil
}

// RenderLaunchdPlistFrom renders the launchd plist template with explicit data.
func RenderLaunchdPlistFrom(data installData) (string, error) {
	buf := &bytes.Buffer{}
	if err := launchdPlistTemplate.Execute(buf, &data); err != nil {
		return "", fmt.Errorf("scheduler.RenderLaunchdPlistFrom: %w", err)
	}
	return buf.String(), nil
}

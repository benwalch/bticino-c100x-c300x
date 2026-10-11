// Package system groups system actions (restarting the bridge, rebooting
// the device) shared by the MQTT and web frontends. The logic comes
// from the MQTT handlers in v0.17.0 (tested on device).
package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

// RestartBridgeAsync relaunches the current binary detached (setsid) after a
// brief pause and terminates this process. There is a ~2s gap with no instance
// (no overlap of two instances at once).
func RestartBridgeAsync(logger *logrus.Logger) {
	logger.Warn("system: bridge restart requested")

	exe, err := os.Executable()
	if err != nil {
		logger.WithError(err).Error("restart_bridge: could not determine executable")
		return
	}

	dir := filepath.Dir(exe)
	args := append([]string(nil), os.Args[1:]...)

	go func() {
		// Start the replacement after the current process has exited.
		script := `sleep 2; cd "$1" || exit 1; shift; exec "$@" > /var/log/bticino_bridge.log 2>&1`
        cmdArgs := []string{"-c", script, "bridge-restart", dir, exe}
        cmdArgs = append(cmdArgs, args...)
        cmd := exec.Command("setsid", append([]string{"sh"}, cmdArgs...)...)

		if err := cmd.Start(); err != nil {
			logger.WithError(err).Error("restart_bridge: could not launch replacement")
			return
		}

		logger.Warn("restart_bridge: replacement scheduled; exiting in 500ms")
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}

// RebootDeviceAsync reboots the whole video door phone (/sbin/reboot) after a
// brief pause (gives time to reply to the requesting client).
func RebootDeviceAsync(logger *logrus.Logger) {
	logger.Warn("system: DEVICE REBOOT requested (/sbin/reboot)")
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := exec.Command("/sbin/reboot").Run(); err != nil {
			logger.WithError(err).Error("reboot: /sbin/reboot failed")
		}
	}()
}

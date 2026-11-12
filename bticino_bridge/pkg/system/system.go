// Package system groups system actions (restarting the bridge, rebooting
// the device) shared by the MQTT and web frontends. The logic comes
// from the MQTT handlers in v0.17.0 (tested on device).
package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/logging"
)

// restartScript starts the replacement once the old process ($1) is gone,
// killing it if the graceful shutdown takes longer than 15 s.
const restartScript = `pid=$1; shift; i=0
while kill -0 "$pid" 2> /dev/null && [ $i -lt 15 ]; do sleep 1; i=$((i + 1)); done
kill -9 "$pid" 2> /dev/null
cd "$1" || exit 1; shift; exec "$@" > /dev/null 2>&1`

// RestartBridgeAsync launches a detached (setsid) replacement and shuts this
// process down gracefully with SIGTERM, so sessions and MQTT close cleanly.
func RestartBridgeAsync(logger *logrus.Logger) {
	logger.Warn("system: bridge restart requested")

	exe, err := os.Executable()
	if err != nil {
		logger.WithError(err).Error("restart_bridge: could not determine executable")
		return
	}

	go func() {
		cmdArgs := []string{"sh", "-c", restartScript, "bridge-restart", strconv.Itoa(os.Getpid()), filepath.Dir(exe), exe}
		cmdArgs = append(cmdArgs, os.Args[1:]...)
		if err := exec.Command("setsid", cmdArgs...).Start(); err != nil {
			logger.WithError(err).Error("restart_bridge: could not launch replacement")
			return
		}

		logger.Warn("restart_bridge: replacement scheduled; shutting down in 500ms")
		time.Sleep(500 * time.Millisecond) // lets the requesting client get its reply
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
}

// RebootDeviceAsync reboots the whole video door phone (/sbin/reboot) after a
// brief pause (gives time to reply to the requesting client). source ("web",
// "mqtt") goes to the crash log on flash, as the log itself is lost on reboot.
func RebootDeviceAsync(logger *logrus.Logger, source string) {
	logger.WithField("source", source).Warn("system: DEVICE REBOOT requested (/sbin/reboot)")
	logging.RecordNote("device reboot requested via " + source)
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := exec.Command("/sbin/reboot").Run(); err != nil {
			logger.WithError(err).Error("reboot: /sbin/reboot failed")
		}
	}()
}

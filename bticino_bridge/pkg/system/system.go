// Package system groups system actions (restarting the bridge, rebooting
// the device) shared by the MQTT and web frontends. The logic comes
// from the MQTT handlers in v0.17.0 (tested on device).
package system

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

// RestartBridgeAsync relanza el binario actual desacoplado (setsid) tras una
// breve pausa y termina este proceso. Hay un hueco de ~2s sin instancia (sin
// solape de dos instancias a la vez).
func RestartBridgeAsync(logger *logrus.Logger) {
	logger.Warn("system: reinicio del bridge solicitado")
	exe, err := os.Executable()
	if err != nil {
		logger.WithError(err).Error("restart_bridge: no se pudo determinar el binario")
		return
	}
	dir := filepath.Dir(exe)
	go func() {
		// setsid + sleep para que el hijo sobreviva a nuestra salida y arranque
		// cuando ya hayamos terminado.
		cmd := exec.Command("setsid", "sh", "-c",
			fmt.Sprintf("sleep 2; cd '%s' && exec '%s' > /tmp/bridge_deploy.log 2>&1", dir, exe))
		if err := cmd.Start(); err != nil {
			logger.WithError(err).Error("restart_bridge: no se pudo relanzar")
			return
		}
		logger.Warn("restart_bridge: relanzado, saliendo en 500ms")
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

package webserver

import (
	"net/http"

	"bticino_bridge/pkg/system"
)

// Injectable system actions (tests replace them with stubs).
// Initialized in NewWebServer; when nil, handlers use pkg/system.
func (ws *WebServer) restartBridge() {
	if ws.restartBridgeFunc != nil {
		ws.restartBridgeFunc()
		return
	}
	system.RestartBridgeAsync(ws.logger)
}

func (ws *WebServer) rebootDevice() {
	if ws.rebootDeviceFunc != nil {
		ws.rebootDeviceFunc()
		return
	}
	system.RebootDeviceAsync(ws.logger)
}

// @Summary Restart the bridge process
// @Description Relaunches the bridge binary detached and exits (≈2s gap)
// @Tags System
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/system/restart-bridge [post]
func (ws *WebServer) handleAPISystemRestartBridge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ws.logger.Warn("Web: bridge restart requested from Controls")
	ws.restartBridge()
	ws.writeJSON(w, map[string]interface{}{
		"success": true,
		"message": "Bridge restarting (≈2s gap)",
	})
}

// @Summary Reboot the whole device
// @Description Runs /sbin/reboot on the intercom after a short delay
// @Tags System
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/system/reboot [post]
func (ws *WebServer) handleAPISystemReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ws.logger.Warn("Web: DEVICE REBOOT requested from Controls")
	ws.rebootDevice()
	ws.writeJSON(w, map[string]interface{}{
		"success": true,
		"message": "Device rebooting",
	})
}

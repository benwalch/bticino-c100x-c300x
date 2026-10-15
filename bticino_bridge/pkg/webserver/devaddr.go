package webserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"bticino_bridge/pkg/sip"
)

// NativeDevAddrVerifyFunc test-dials one INVITE with the candidate address.
type NativeDevAddrVerifyFunc func(candidate string) error

// SetNativeDevAddrSniffer sets the passive native dev addr discoverer.
func (ws *WebServer) SetNativeDevAddrSniffer(sniffer *sip.NativeDevAddrDiscoverer) {
	ws.nativeDevAddrSniffer = sniffer
}

// SetNativeDevAddrVerifyFunc sets the candidate verification dial.
func (ws *WebServer) SetNativeDevAddrVerifyFunc(fn NativeDevAddrVerifyFunc) {
	ws.nativeDevAddrVerify = fn
}

// SetNativeDevAddrSessionLive reports whether a camera session is currently up.
func (ws *WebServer) SetNativeDevAddrSessionLive(fn func() bool) {
	ws.nativeDevAddrSessionLive = fn
}

// @Summary Discover SIP domain from local Flexisip config
// @Description Read-only: parses aliases= from the on-device Flexisip config. Apply via the normal save flow.
// @Tags SIP
// @Produce json
// @Router /api/sip/domain/discover [get]
func (ws *WebServer) handleAPISIPDomainDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	value, source, err := sip.ReadFlexisipDomain()
	if err != nil {
		ws.writeJSON(w, map[string]interface{}{"value": "", "error": err.Error()})
		return
	}
	ws.writeJSON(w, map[string]interface{}{"value": value, "source": source})
}

// @Summary Current native dev addr value and source
// @Tags SIP
// @Produce json
// @Router /api/sip/native-dev-addr [get]
func (ws *WebServer) handleAPINativeDevAddr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	value := ws.config.SIP.NativeDevAddr
	source := "configured"
	if value == "" {
		value = sip.DefaultNativeDevAddr()
		source = "default"
	}
	ws.writeJSON(w, map[string]interface{}{"value": value, "source": source})
}

// @Summary Arm native dev addr discovery (passive, 30s)
// @Description Listens on the loopback SIP leg for the next genuine dialog carrying a=DEVADDR (place an app call). Requires POST + ?confirm=yes.
// @Tags SIP
// @Produce json
// @Router /api/sip/native-dev-addr/discover [post]
func (ws *WebServer) handleAPINativeDevAddrDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed (use POST)", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("confirm") != "yes" {
		http.Error(w, "confirm required: POST /api/sip/native-dev-addr/discover?confirm=yes", http.StatusBadRequest)
		return
	}
	if ws.nativeDevAddrSniffer == nil {
		http.Error(w, "discovery unavailable", http.StatusServiceUnavailable)
		return
	}
	if ws.nativeDevAddrSessionLive != nil && ws.nativeDevAddrSessionLive() {
		ws.writeJSON(w, map[string]interface{}{"state": "busy", "message": "a camera session is running — app calls will fail; stop it first"})
		return
	}
	if err := ws.nativeDevAddrSniffer.Start(sip.DiscoverTimeout()); err != nil {
		ws.writeJSON(w, map[string]interface{}{"state": "busy", "error": err.Error()})
		return
	}
	ws.writeJSON(w, map[string]interface{}{"state": "waiting"})
}

// @Summary Native dev addr discovery status
// @Tags SIP
// @Produce json
// @Router /api/sip/native-dev-addr/discover/status [get]
func (ws *WebServer) handleAPINativeDevAddrDiscoverStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.nativeDevAddrSniffer == nil {
		http.Error(w, "discovery unavailable", http.StatusServiceUnavailable)
		return
	}
	state, value := ws.nativeDevAddrSniffer.Status()
	ws.writeJSON(w, map[string]interface{}{"state": state, "value": value})
}

// @Summary Cancel native dev addr discovery
// @Tags SIP
// @Produce json
// @Router /api/sip/native-dev-addr/discover/cancel [post]
func (ws *WebServer) handleAPINativeDevAddrDiscoverCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed (use POST)", http.StatusMethodNotAllowed)
		return
	}
	if ws.nativeDevAddrSniffer == nil {
		http.Error(w, "discovery unavailable", http.StatusServiceUnavailable)
		return
	}
	ws.nativeDevAddrSniffer.Stop()
	ws.writeJSON(w, map[string]interface{}{"state": "idle"})
}

// @Summary Verify a native dev addr candidate (one test INVITE + BYE)
// @Description Dials the native unit with the candidate address: 180/200 accepts it, an instant 486 rejects it. Requires POST + ?confirm=yes.
// @Tags SIP
// @Produce json
// @Router /api/sip/native-dev-addr/verify [post]
func (ws *WebServer) handleAPINativeDevAddrVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed (use POST)", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("confirm") != "yes" {
		http.Error(w, "confirm required: POST /api/sip/native-dev-addr/verify?confirm=yes", http.StatusBadRequest)
		return
	}
	if ws.nativeDevAddrVerify == nil {
		http.Error(w, "verification unavailable (no SIP client)", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Value == "" {
		http.Error(w, "JSON body with non-empty value required", http.StatusBadRequest)
		return
	}
	if err := ws.nativeDevAddrVerify(body.Value); err != nil {
		ws.writeJSON(w, map[string]interface{}{"valid": false, "busy": errors.Is(err, sip.ErrNativeBusy), "error": err.Error()})
		return
	}
	ws.writeJSON(w, map[string]interface{}{"valid": true, "value": body.Value})
}

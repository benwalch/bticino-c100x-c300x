# BTicino Bridge - Single Configuration

**IMPORTANT**: There is only ONE valid configuration file:

```
config.yaml  ← ONLY configuration file
```

## 📁 Structure

```
configs/
├── config.yaml              # ← ONLY configuration file
└── homeassistant/           # Home Assistant configurations (optional)
```

## 🚀 Usage

### **On your development machine**:
```bash
# The configs/config.yaml file is used as a template
# For deploy, copy to the device
```

### **On the device**:
```bash
# Location: /home/bticino/cfg/extra/config.yaml
# The bridge starts with:
./bticino_bridge -config config.yaml
```

## 📝 Configuration Sections

1. **Bridge** - Name, version, log level
2. **OpenWebNet** - OpenWebNet bus connection
3. **SIP** - SIP server configuration (local or external flexisip)
4. **MQTT** - Home Assistant connection
5. **Web** - Web dashboard (port, directories)
6. **HomeKit** - Apple HomeKit integration
7. **Hardware** - GPIO, input devices
8. **Logging** - Log configuration
9. **Streaming** - RTSP/WebRTC for video
10. **Network** - NTP, DNS, firewall
11. **Servers** - External servers (cloud, updates, SIP)
12. **Notifications** - MQTT, email, push
13. **Privacy** - Privacy and telemetry
14. **Security** - Authentication, HTTPS, rate limiting

## ⚠️ Historical Files (REMOVED)

These files NO LONGER exist; they were consolidated into `config.yaml`:

- ❌ `config_ha.yaml` - Removed
- ❌ `config_production_homekit.yaml` - Removed
- ❌ `config-streaming-example.yaml` - Removed
- ❌ `config_with_servers.yaml` - Removed
- ❌ `config_streaming.yaml` (device) - Removed

## 🔄 Backup

Backups are created automatically in:
```
/home/bticino/cfg/extra/backups/config_YYYYMMDD_HHMMSS.yaml
```

## 🛠️ Editing

### **From the Web UI** (Recommended):
```
http://192.168.1.38:8082/settings
```

### **Manual** (SSH):
```bash
ssh bticino
cd /home/bticino/cfg/extra
nano config.yaml
# Restart bridge
pkill -9 bticino_bridge
./bticino_bridge -config config.yaml &
```

---

**Last updated**: 2026-03-26  
**Version**: v0.13.0

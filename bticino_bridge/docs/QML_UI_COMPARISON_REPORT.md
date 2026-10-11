# BTicino UI Comparison Report - Web vs QML

**Date**: 2026-03-26  
**Web UI Version**: v0.14.1  
**QML UI Version**: Stock (2018-2020)  
**Status**: 📊 **ANALYSIS COMPLETE**

---

## 📊 Executive Summary

| Feature | Web UI (bticino_bridge) | QML UI (Device) | Synchronization |
|----------------|------------------------|---------------------|----------------|
| **Technology** | HTML5/CSS3/JavaScript | Qt Quick 1.1 / QML | ❌ Different |
| **Access** | http://device:8082 | Device touchscreen | ✅ Complementary |
| **Configuration** | 12 full tabs | Limited menus | ✅ Web more complete |
| **Editing** | ✅ Full | ⚠️ Limited | ✅ Web preferred |
| **Backup/Restore** | ✅ Implemented | ❌ Not available | ✅ Web only |
| **Languages** | English/Spanish | Multi-language (Qt) | ⚠️ Web needs improvement |

---

## 🎯 Detailed Comparison

### **1. Menu Structure**

#### **Web UI (12 tabs)**:
```
Settings
├── Bridge
├── Device
├── OpenWebNet
├── SIP
├── MQTT
├── HomeKit
├── Hardware
├── Streaming
├── Audio
├── Display
├── Privacy
└── Security
```

#### **QML UI (Settings.qml)**:
```
Settings
├── WiFi Settings
├── DateTime Settings
├── Languages Settings
├── Accounts Settings
├── Ringtones
├── Answering Machine
├── Quick Actions
└── Configuration Menu
    ├── Add Intercom
    ├── Add Camera
    └── Add Actuation
```

**Conclusion**: 
- ✅ **Web UI**: more focused on technical configuration (OpenWebNet, MQTT, SIP)
- ✅ **QML UI**: more focused on end users (doorbell, language, date)
- ✅ **Both**: complementary, not conflicting

---

### **2. Existing QML Files**

**Total**: 104 `.qml` files

#### **Main**:
| QML File | Purpose | Web Equivalent |
|-------------|-----------|-----------------|
| `Settings.qml` | Main configuration menu | `/settings` |
| `ConfigurationMenu.qml` | Configure devices | `/settings/device` |
| `WiFiSettings.qml` | Configure WiFi | ❌ Not available on Web |
| `DateTimeSettings.qml` | Date and time | ❌ Not available on Web |
| `LanguagesSettings.qml` | Languages | ❌ Not available on Web |
| `AccountsSettings.qml` | SIP accounts | `/settings/sip` ✅ |
| `RingtonesExtra.qml` | Ringtones | ❌ Not available on Web |
| `KeyPad.qml` | Numeric keypad | ❌ Not available on Web |
| `VideoCallPage.qml` | Video calls | ❌ Not available on Web |
| `AudioCallPage.qml` | Audio calls | ❌ Not available on Web |

---

### **3. Synchronization Status**

#### **✅ WHAT IS SYNCHRONIZED**:

1. **SIP configuration**:
    - QML: `AccountsSettings.qml` → Edits SIP users
    - Web: `/settings/sip` → Edits full SIP configuration
    - **Sync**: ✅ both edit `config.yaml`

2. **Network configuration**:
    - QML: `WiFiSettings.qml` → WiFi
    - Web: `/settings/device` → IP, DNS, NTP
    - **Sync**: ✅ same configuration file

3. **Answering Machine**:
    - QML: `AnsweringPage.qml` → Answering machine
    - Web: `/settings/audio` → Answering machine + recording
    - **Sync**: ✅ same backend

#### **⚠️ WHAT IS NOT SYNCHRONIZED**:

1. **Language**:
    - QML: `LanguagesSettings.qml` → Changes the device language
    - Web: English/Spanish only in UI
    - **Gap**: Web should read the device language

2. **Date/Time**:
    - QML: `DateTimeSettings.qml` → NTP, time zone
    - Web: Not available
    - **Gap**: Web could show/edit NTP

3. **Ringtones**:
    - QML: `RingtonesExtra.qml` → Selects ringtone
    - Web: Not available
    - **Gap**: Web could show available ringtones

4. **Quick Actions**:
    - QML: quick actions on main screen
    - Web: Not available
    - **Gap**: Web could configure quick actions

---

## 🔍 Technical Analysis

### **QML Architecture**:

```qml
// Settings.qml (extracto)
import QtQuick 1.1
import Components 1.0
import Components.Settings 1.0
import BtObjects 2.0

Page {
    id: page
    objectName: "pageBackgroundSettings"
    showBackButton: true
    headerLabel: qsTr("Settings")

    // Configura objetos globales
    function checkAnsweringMenu() {
        if (global.answeringMachine && global.guiSettings.answeringMachineVisible)
            return true
    }
}
```

**Notes**:
- Qt Quick 1.1 (old version, 2011-2013)
- Uses global objects (`global.answeringMachine`)
- Translations with `qsTr()` and `trsl.empty`
- No direct connection to the bridge's `config.yaml`

### **Web Architecture**:

```javascript
// config_ui.js (extracto)
document.addEventListener('DOMContentLoaded', function() {
    loadConfig();
    setupEventListeners();
});

async function loadConfig() {
    const response = await fetch('/api/config');
    const data = await response.json();
    populateForm(data.config);
}
```

**Notes**:
- Modern JavaScript (ES6+)
- Reads configuration from REST API
- Saves to `config.yaml`
- No connection to QML UI

---

## 📝 Conclusions

### **✅ CURRENT STRENGTHS**:

1. **Web UI**:
    - ✅ More complete for technical configuration
    - ✅ Automatic Backup/Restore
    - ✅ REST API for automation
    - ✅ No touchscreen required

2. **QML UI**:
    - ✅ Integrated in device
    - ✅ Native multi-language
    - ✅ Access without network
    - ✅ More polished UI (Qt Quick)

### **⚠️ WEAKNESSES**:

1. **Web UI**:
    - ❌ No full multi-language
    - ❌ No access to display settings
    - ❌ No control of native ringtones

2. **QML UI**:
    - ❌ No backup/restore
    - ❌ No API for automation
    - ❌ Limited technical configuration
    - ❌ Qt Quick 1.1 (obsolete)

### **🎯 IMPROVEMENT OPPORTUNITIES**:

#### **Short Term** (Web UI):
1. ✅ Add "Device" tab for NTP/Date/Time
2. ✅ Read language from device
3. ✅ Show available ringtones

#### **Medium Term** (QML + Web):
1. ⏳ Create shared configuration file
2. ⏳ Notify changes between UIs
3. ⏳ Sync state in real time

#### **Long Term** (Custom QML):
1. ⏳ Create custom "bticino_bridge" QML skin
2. ⏳ Add tabs for MQTT, HomeKit, Streaming
3. ⏳ Integrate REST API into QML

---

## 🚀 Future QML Improvements

### **Idea 1: bticino_bridge QML Skin**

Create a custom QML file that integrates with the native UI:

```qml
// bticino_bridge.qml
import QtQuick 1.1
import Components 1.0
import BtObjects 2.0

Page {
    id: page
    headerLabel: qsTr("BTicino Bridge")
    
    // Configure MQTT
    Rectangle {
        Label { text: "MQTT Broker" }
        TextInput { 
            text: global.bticinoBridge.mqttHost
            onTextChanged: global.bticinoBridge.mqttHost = text
        }
    }
    
    // Configure HomeKit
    Rectangle {
        Label { text: "HomeKit PIN" }
        TextInput { 
            text: global.bticinoBridge.homekitPin
            onTextChanged: global.bticinoBridge.homekitPin = text
        }
    }
}
```

**Requirements**:
- Compile QML skin (needs Qt toolchain)
- Integrate with `BtObjects` (device global objects)
- Test on real device

**Difficulty**: 🔴 **HIGH** (requires reverse engineering of BtObjects)

---

### **Idea 2: REST API for QML**

Expose configuration via API so QML can read it:

```cpp
// En bt_daemon o proceso nativo
// Expose global object for QML
class BticinoBridge : public QObject {
    Q_OBJECT
    Q_PROPERTY(QString mqttHost READ mqttHost WRITE setMqttHost)
    Q_PROPERTY(QString homekitPin READ homekitPin WRITE setHomekitPin)
    
public:
    QString mqttHost() const { return m_mqttHost; }
    void setMqttHost(const QString &host) { 
        m_mqttHost = host; 
        saveConfig(); 
    }
    
private:
    QString m_mqttHost;
};

// Registrar en QML
qmlRegisterType<BticinoBridge>("BtObjects", 2, 0, "BticinoBridge");
```

**Requirements**:
- Modify `bt_daemon` or native process
- Compile for ARM
- Test on device

**Difficulty**: 🟡 **MEDIUM** (requires C++ and Qt)

---

### **Idea 3: Polling from Web UI**

The web UI can read existing QML configuration:

```javascript
// Read language from device
async function getDeviceLanguage() {
    const response = await ssh('cat /home/bticino/.language');
    return response.trim();
}

// Read current doorbell
async function getCurrentRingtone() {
    const response = await ssh('cat /home/bticino/cfg/extra/ringtone.conf');
    return response.trim();
}
```

**Requirements**:
- SSH from bridge to device (localhost)
- Parse QML configuration files
- Show in web UI

**Difficulty**: 🟢 **LOW** (JavaScript only)

---

## 📋 Recommendations

### **Priority 1** (Immediate):
1. ✅ Document existing QML structure
2. ✅ Identify QML configuration files
3. ✅ Create API to read QML configuration from Web

### **Priority 2** (Short Term):
1. ⏳ Add "Device" tab in Web UI (NTP, Date, Time)
2. ⏳ Read language from device
3. ⏳ Show ringtone configuration

### **Priority 3** (Medium Term):
1. ⏳ Investigate `BtObjects` in QML
2. ⏳ Create basic "bticino_bridge" QML skin
3. ⏳ Integrate REST API into QML

### **Priority 4** (Long Term):
1. ⏳ Full QML skin with all Web features
2. ⏳ Real-time bidirectional sync
3. ⏳ Replace stock QML UI with custom

---

## 📁 Reference Files

### **QML Stock**:
```
/home/bticino/bin/gui/skins/default/
├── Settings.qml (8 KB)
├── ConfigurationMenu.qml (1.5 KB)
├── WiFiSettings.qml (26 KB)
├── DateTimeSettings.qml (10 KB)
├── LanguagesSettings.qml (2.5 KB)
├── AccountsSettings.qml (4 KB)
├── RingtonesExtra.qml
├── KeyPad.qml
└── Components/
    ├── Styles/
    └── Settings/
```

### **Web UI**:
```
pkg/webserver/
├── server.go (4669 lines)
├── config_manager.go (449 lines)
├── config_handlers.go (250 lines)
├── config_ui.js (500+ embedded lines)
└── config_ui.css (300+ embedded lines)
```

### **Configuration**:
```
/home/bticino/cfg/extra/config.yaml (4.4 KB)
← ONLY shared configuration file
```

---

**Status**: 📊 **ANALYSIS COMPLETE**  
**Next Step**: Implement Priority 1 (read QML configuration from Web)  
**Overall Difficulty**: 🟡 **MEDIUM** (old QML, but documented)

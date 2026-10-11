# Configuration Management via Web Dashboard - Feasibility Analysis

**Date**: 2026-03-24  
**Version**: bticino_bridge v0.12.0  
**Type**: architectural evolution analysis

---

## 📊 Executive Summary

**Is it a coherent evolution?** → ✅ **YES, ABSOLUTELY**

Managing configuration via the web dashboard is a **natural and coherent evolution** for the bticino_bridge project, especially considering:

1. ✅ **The infrastructure already exists** (web server, REST API, settings page)
2. ✅ **Solves real problems** (SSH deploy, manual YAML editing)
3. ✅ **Aligned with the architecture** (everything is managed via web except config)
4. ✅ **Significantly improves user experience**
5. ✅ **Low technical risk** (config is already in memory, only persistence is missing)

---

## 🎯 Current State vs Desired State

### **Current State (v0.12.0)**

```
┌─────────────────────────────────────────────────────────┐
│  Web Dashboard (:8082)                                  │
│  ✅ Dashboard - System status                        │
│  ✅ Messages - Message management                     │
│  ✅ Controls - Device control                         │
│  ✅ Settings - READ ONLY (static HTML)                │
│  ✅ Logs - Log viewer                                 │
└─────────────────────────────────────────────────────────┘
                            │
                            ▼
              ┌─────────────────────────┐
              │  API REST               │
              │  ✅ 24 endpoints        │
              │  ❌ No POST endpoints   │
              │     for configuration   │
              └─────────────────────────┘
                            │
                            ▼
              ┌─────────────────────────┐
              │  config.yaml            │
              │  Manual editing via SSH │
              │  Requires restart       │
              └─────────────────────────┘
```

**Problems**:
- ❌ Editing config requires SSH + text editor
- ❌ Requires manual service restart
- ❌ No YAML syntax validation
- ❌ No automatic backup
- ❌ No easy rollback
- ❌ Error-prone for non-technical users

---

### **Desired State (v0.13.0)**

```
┌─────────────────────────────────────────────────────────┐
│  Web Dashboard (:8082)                                  │
│  ✅ Dashboard - System status                      │
│  ✅ Messages - Message management                    │
│  ✅ Controls - Device control                        │
│  ✅ Settings - EDITABLE (forms + validation)         │
│  ✅ Logs - Log viewer                                │
│  ✅ Config - Full management (CRUD)                  │
└─────────────────────────────────────────────────────────┘
                            │
                            ▼
              ┌─────────────────────────┐
              │  API REST               │
              │  ✅ GET /api/config     │
              │  ✅ POST /api/config    │
              │  ✅ POST /api/config/validate │
              │  ✅ POST /api/config/backup │
              │  ✅ POST /api/config/restart │
              └─────────────────────────┘
                            │
                            ▼
              ┌─────────────────────────┐
              │  Config Manager         │
              │  ✅ YAML validation   │
              │  ✅ Automatic backup    │
              │  ✅ Hot reload          │
              │  ✅ Rollback            │
              └─────────────────────────┘
                            │
                            ▼
              ┌─────────────────────────┐
              │  config.yaml            │
              │  Automatic update     │
              │  Backup: config.yaml.bak │
              └─────────────────────────┘
```

**Benefits**:
- ✅ Editing from the browser (no SSH)
- ✅ Real-time validation
- ✅ Automatic backup before changes
- ✅ Hot reload (automatic restart)
- ✅ 1-click rollback
- ✅ Accessible to non-technical users

---

## 🔍 Technical Analysis

### 1. **Existing Infrastructure** ✅

| Component | Status | Reuse |
|------------|--------|---------------|
| Web Server | ✅ Implemented | 100% reusable |
| API REST | ✅ 24 endpoints | Existing pattern |
| Settings Page | ✅ HTML exists | Extend with forms |
| Config in Memory | ✅ `ws.config` | Already available |
| YAML Parser | ✅ `gopkg.in/yaml.v2` | Already imported |

**Code to write**: ~500-800 lines (estimated)

---

### 2. **Required Endpoints**

```go
// GET /api/config - Get current configuration
GET /api/config
Response: {
  "config": { ... },
  "sections": ["bridge", "openwebnet", "sip", "mqtt", ...],
  "timestamp": "2026-03-24T10:00:00Z"
}

// POST /api/config - Save configuration
POST /api/config
Body: { "config": { ... } }
Response: {
  "success": true,
  "message": "Config saved",
  "backup": "config.yaml.20260324_100000.bak",
  "restart_required": true
}

// POST /api/config/validate - Validate configuration
POST /api/config/validate
Body: { "config": { ... } }
Response: {
  "valid": true,
  "errors": [],
  "warnings": ["SIP server not reachable"]
}

// POST /api/config/backup - Create backup
POST /api/config/backup
Response: {
  "success": true,
  "backup_file": "config.yaml.20260324_100000.bak"
}

// POST /api/config/restore - Restaurar backup
POST /api/config/restore
Body: { "backup_file": "config.yaml.20260324_100000.bak" }
Response: {
  "success": true,
  "message": "Config restored"
}

// POST /api/config/restart - Restart service
POST /api/config/restart
Response: {
  "success": true,
  "message": "Restarting..."
}

// GET /api/config/history - Historial de cambios
GET /api/config/history
Response: {
  "history": [
    {"timestamp": "...", "file": "...", "changes": "..."}
  ]
}
```

---

### 3. **UI/UX - Settings Page**

**Editable sections**:

```
┌─────────────────────────────────────────────────────────┐
│  Settings - bticino_bridge v0.13.0                      │
├─────────────────────────────────────────────────────────┤
│  [Bridge] [OpenWebNet] [SIP] [MQTT] [Web] [Streaming]   │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  === SIP Configuration ===                              │
│  ┌─────────────────────────────────────────────────┐   │
│  │ Enabled:        [✓] Enable SIP                  │   │
│  │ Server Host:    [192.168.1.38         ]         │   │
│  │ Server Port:    [5060                 ]         │   │
│  │ Transport:      [TCP ▼]                         │   │
│  │ Domain:         [YOUR_GATEWAY_ID.bs.iotleg.com]         │   │
│  │ Username:       [baresip@...]         ]         │   │
│  │ Password:       [••••••••             ] [Show]  │   │
│  │ Use HA1:        [ ] Use pre-computed hash       │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  [Validate] [Save] [Backup] [Cancel]                    │
│                                                         │
│  Last saved: 2026-03-24 10:00:00 by admin              │
│  Backups available: 5                                   │
└─────────────────────────────────────────────────────────┘
```

---

### 4. **Proposed Architecture**

```go
// pkg/webserver/config_manager.go

type ConfigManager struct {
    configPath     string
    backupPath     string
    maxBackups     int
    config         *config.Config
    configMutex    sync.RWMutex
    changeHistory  []ConfigChange
    logger         *logrus.Logger
}

type ConfigChange struct {
    Timestamp   time.Time
    User        string
    Changes     map[string]interface{}
    BackupFile  string
    Restarted   bool
}

// Methods:
// - LoadConfig() *Config
// - SaveConfig(*Config) error
// - ValidateConfig(*Config) ([]error, []warning)
// - CreateBackup() (string, error)
// - RestoreBackup(string) error
// - GetHistory() []ConfigChange
// - HotReload() error
```

---

## 📈 Risk Analysis

### **Technical Risks**

| Risk | Probability | Impact | Mitigation |
|--------|--------------|---------|------------|
| Invalid config breaks service | Medium | High | Validation + automatic backup |
| Hot reload fails | Medium | Medium | Manual restart as fallback |
| Configuration loss | Low | High | Multiple backups + history |
| Race conditions | Medium | Medium | Mutex + transactions |
| Security (auth) | High | High | Mandatory login + HTTPS |

---

### **Security Risks**

```yaml
# CRITICAL: The configuration contains credentials
sip:
  password: "sensitive"
mqtt:
  password: "sensitive"
  
# Requiere:
1. Mandatory authentication for /api/config
2. HTTPS in production
3. Password encryption in UI
4. Audit log de cambios
5. Rate limiting en endpoints
```

---

## 🎯 Proposed Implementation

### **Phase 1: Basic API (v0.13.0-alpha)**
- [ ] GET /api/config
- [ ] POST /api/config (no validation)
- [ ] Manual backup before saving
- [ ] Settings page with basic forms

**Lines of code**: ~300  
**Estimated time**: 4-6 hours  
**Risk**: Low

---

### **Phase 2: Validation (v0.13.0-beta)**
- [ ] POST /api/config/validate
- [ ] YAML syntax validation
- [ ] Semantic validation (ports, IPs)
- [ ] Warnings for risky configs

**Lines of code**: ~200  
**Estimated time**: 3-4 hours  
**Risk**: Low

---

### **Phase 3: Hot Reload (v0.13.0-rc)**
- [ ] POST /api/config/restart
- [ ] Graceful shutdown
- [ ] Auto-restart after saving
- [ ] Health check post-restart

**Lines of code**: ~150  
**Estimated time**: 4-5 hours  
**Risk**: Medium

---

### **Phase 4: Security (v0.13.0)**
- [ ] Authentication for config endpoints
- [ ] Optional HTTPS
- [ ] Change audit log
- [ ] Rate limiting

**Lines of code**: ~250  
**Estimated time**: 6-8 hours  
**Risk**: Medium

---

### **Phase 5: Advanced UX (v0.14.0)**
- [ ] Change history
- [ ] Diff between versions
- [ ] 1-click rollback
- [ ] Config import/export
- [ ] Configuration templates

**Lines of code**: ~400  
**Estimated time**: 8-10 hours  
**Risk**: Low

---

## 📊 Comparison with Alternatives

### **Alternative 1: Keep manual editing (Status Quo)**

| Pros | Cons |
|------|---------|
| ✅ No development | ❌ Requires SSH |
| ✅ No new bugs | ❌ Error-prone |
| | ❌ Not accessible to users |
| | ❌ No validation |
| | ❌ No automatic backup |

**Verdict**: ❌ Not recommended - limits adoption

---

### **Alternative 2: Web Dashboard (Proposed)**

| Pros | Cons |
|------|---------|
| ✅ Accessible from browser | ⚠️ Requires development (~1000 LOC) |
| ✅ Automatic validation | ⚠️ Security risk (mitigable) |
| ✅ Automatic backup | ⚠️ Additional complexity |
| ✅ Hot reload | |
| ✅ Easy rollback | |
| ✅ Audit log | |

**Verdict**: ✅ **RECOMMENDED** - significant UX improvement

---

### **Alternative 3: CLI Tool + Web**

| Pros | Cons |
|------|---------|
| ✅ Reusable in scripts | ⚠️ More development (CLI + Web) |
| ✅ SSH still works | ⚠️ Duplicated logic |
| ✅ Power users happy | ⚠️ Additional maintenance |

**Verdict**: ⚠️ **OPTIONAL** - nice to have, not critical

---

## 🎯 Final Recommendation

### **Is it a coherent evolution?**

**✅ YES** - For the following reasons:

1. **Aligned with the existing architecture**
    - Web dashboard already exists
    - REST API already exists
    - Config is already in memory
    - Only persistence + UI missing

2. **Solves real problems**
    - SSH not always available
    - Manual editing is error-prone
    - Non-technical users need access

3. **Low technical risk**
    - Infrastructure 90% ready
    - YAML validation already exists
    - Hot reload is simple (signal + exec)

4. **High value for users**
    - Reduces configuration time
    - Reduces errors
    - Improves overall experience

5. **Natural project evolution**
    - v0.10.0: Web dashboard (read)
    - v0.11.0: RTSP streaming
    - **v0.12.0: Web dashboard (write)** ← Next logical step
    - v0.13.0: Advanced features (HKSV, etc.)

---

## 📝 Suggested Implementation Plan

### **Sprint 1: Basic API (v0.13.0-alpha)**
```bash
# Semana 1
- [ ] Crear pkg/webserver/config_manager.go
- [ ] Implementar GET /api/config
- [ ] Implementar POST /api/config
- [ ] Automatic backup before saving
- [ ] Settings page with basic forms
```

### **Sprint 2: Validation + Hot Reload (v0.13.0-beta)**
```bash
# Semana 2
- [ ] Implement YAML validation
- [ ] Semantic validation (ports, IPs)
- [ ] POST /api/config/restart
- [ ] Graceful shutdown
- [ ] Health check post-restart
```

### **Sprint 3: Security (v0.13.0-rc)**
```bash
# Semana 3
- [ ] Authentication for endpoints
- [ ] HTTPS opcional
- [ ] Audit log
- [ ] Testing exhaustivo
```

### **Release: v0.13.0**
```bash
# Semana 4
- [ ] Documentation
- [ ] Testing en dispositivo real
- [ ] Release candidate
- [ ] Release final
```

---

## 🏆 Conclusion

**Managing configuration via the web dashboard is:**

1. ✅ **Technically viable** (90% of the infrastructure exists)
2. ✅ **Architecturally coherent** (follows existing patterns)
3. ✅ **Valuable** (significantly improves UX)
4. ✅ **Strategically right** (aligned with roadmap)
5. ✅ **Low risk** (mitigations available)

**Recommendation**: **IMPLEMENT in v0.13.0**

---

**Next steps**:
1. Create GitHub issue: "Web-based configuration management"
2. Prioritize in v0.13.0 roadmap
3. Assign developer (estimated: 3 sprints)
4. Start Sprint 1 (Basic API)

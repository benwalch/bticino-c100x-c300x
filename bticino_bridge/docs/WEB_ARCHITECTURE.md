# BTicino Bridge - Web Architecture Documentation

**Date**: 2026-03-29  
**Version**: 0.14.3  
**Status**: ⚠️ **TRANSITION** (embedded HTML → Svelte)

---

## 📊 **Current Architecture (v0.14.3)**

### **Frontend Status**

| Component | Status | Location |
|------------|--------|-----------|
| **Dashboard** | ⚠️ HTML embedded in Go | `pkg/webserver/server.go` |
| **Settings** | ⚠️ HTML embedded in Go | `pkg/webserver/server.go` |
| **Controls** | ⚠️ HTML embedded in Go | `pkg/webserver/server.go` |
| **Logs** | ⚠️ HTML embedded in Go | `pkg/webserver/server.go` |
| **CSS** | ⚠️ Embedded in Go | `pkg/webserver/server.go` |
| **JavaScript** | ⚠️ Embedded in Go | `pkg/webserver/server.go` |
| **Svelte Build** | ✅ Built | `web/dist/` |
| **Svelte Source** | ✅ Ready | `web/src/` |

---

## 🏗️ **Current Architecture**

```
┌─────────────────────────────────────────────────────────┐
│                   BTicino Bridge                        │
│                                                         │
│  ┌───────────────────────────────────────────────────┐ │
│  │  Go Web Server (pkg/webserver/server.go)          │ │
│  │                                                   │ │
│  │  HTML Templates (embedded in code):        │ │
│  │  ├── getDashboardHTML()                          │ │
│  │  ├── getSettingsHTML()                           │ │
│  │  ├── getControlsHTML()                           │ │
│  │  └── getLogsHTML()                               │ │
│  │                                                   │ │
│  │  CSS (embebido):                                 │ │
│  │  └── getCSS()                                    │ │
│  │                                                   │ │
│  │  JavaScript (embebido):                          │ │
│  │  └── getJS()                                     │ │
│  │                                                   │ │
│  │  API Routes:                                     │ │
│  │  ├── /api/status                                 │ │
│  │  ├── /api/config                                 │ │
│  │  ├── /api/controls/*                             │ │
│  │  └── /api/logs                                   │ │
│  └───────────────────────────────────────────────────┘ │
│                                                         │
│  ┌───────────────────────────────────────────────────┐ │
│  │  Svelte Build (web/dist/) - ⚠️ NO USADO          │ │
│  │  ├── index.html                                   │ │
│  │  └── assets/                                      │ │
│  │      ├── index-*.js                               │ │
│  │      ├── vendor-*.js                              │ │
│  │      └── index-*.css                              │ │
│  └───────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

---

## 🔍 **How It Currently Works**

### **1. Server Startup**

```go
// pkg/webserver/server.go
func NewWebServer(cfg *config.Config, bridge BTicinoBridge, logger *logrus.Logger) *WebServer {
    return &WebServer{
        config:        cfg,
        bridge:        bridge,
        logger:        logger,
        staticDir:     cfg.Web.StaticDir,  // ⚠️ No se usa para HTML
        messageParser: messageparser.NewMessageParser(),
        // ...
    }
}
```

### **2. Route Registration**

```go
// Routes for embedded HTML
mux.HandleFunc("/", ws.handleDashboard)
mux.HandleFunc("/dashboard", ws.handleDashboard)
mux.HandleFunc("/messages", ws.handleMessagesPage)
mux.HandleFunc("/controls", ws.handleControlsPage)
mux.HandleFunc("/settings", ws.handleSettingsPage)
mux.HandleFunc("/logs", ws.handleLogsPage)

// Routes for API
mux.HandleFunc("/api/status", ws.handleAPIStatus)
mux.HandleFunc("/api/config", ws.handleAPIConfig)
// ...
```

### **3. HTML Rendering**

```go
// HTML is generated from strings embedded in Go
func (ws *WebServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/html")
    w.Write([]byte(ws.injectVersion(ws.getDashboardHTML())))
}

func (ws *WebServer) getDashboardHTML() string {
    return `<!DOCTYPE html>
<html lang="en">
<head>
    <title>BTicino Bridge {{VERSION}}</title>
    ...
</head>
<body>
    <!-- HTML embedded in Go code -->
    ...
</body>
</html>`
}
```

### **4. CSS and JavaScript**

```go
// CSS embebido
func (ws *WebServer) getCSS() string {
    return `/* Dashboard Styles */
:root {
    --primary-color: #2196F3;
    ...
}
...`
}

// JavaScript embebido
func (ws *WebServer) getJS() string {
    return `// Dashboard JavaScript
async function refreshStatus() {
    const response = await fetch('/api/status');
    ...
}
...`
}
```

---

## 📁 **Current File Structure**

```
bticino_bridge/
├── pkg/
│   └── webserver/
│       ├── server.go              # ⚠️ ALL embedded HTML/CSS/JS (~4800 lines)
│       ├── config_manager.go      # ✅ Configuration management
│       ├── config_handlers.go     # ✅ API handlers
│       └── device_handlers.go     # ✅ Device API handlers
├── web/
│   ├── src/
│   │   ├── routes/
│   │   │   ├── +page.svelte       # ✅ Dashboard Svelte
│   │   │   ├── settings/
│   │   │   │   └── +page.svelte   # ✅ Settings Svelte
│   │   │   ├── controls/
│   │   │   │   └── +page.svelte   # ✅ Controls Svelte
│   │   │   └── logs/
│   │   │       └── +page.svelte   # ✅ Logs Svelte
│   │   └── App.svelte             # ✅ App shell
│   ├── dist/                      # ⚠️ Compilado pero NO USADO
│   │   ├── index.html
│   │   └── assets/
│   ├── package.json
│   └── vite.config.js
└── configs/
    └── config.yaml
```

---

## ⚠️ **Problems with the Current Architecture**

### **1. Monolithic Code**

**server.go**: ~4800 lines
- Embedded HTML: ~3000 lines
- Embedded CSS: ~1000 lines
- Embedded JavaScript: ~500 lines
- Go logic: ~300 lines

**Problem**: hard to maintain, no syntax highlighting for HTML/CSS/JS.

### **2. No Hot Reload**

- Every change requires: `go build` → `./scripts/deploy.sh`
- No agile development
- Feedback time: ~30 seconds

### **3. No Separation of Concerns**

- HTML, CSS, JS and Go mixed in a single file
- No componentization
- No code reuse

### **4. Build Size**

- Go binary: 14MB (with embedded HTML)
- No code splitting
- No lazy loading

### **5. Built Svelte Not Used**

```
web/dist/ existe pero el servidor no lo usa
```

---

## 🎯 **Target Architecture (Svelte)**

### **Desired State**

| Component | Current | Target |
|------------|--------|----------|
| **Dashboard** | Embedded HTML | `web/src/routes/+page.svelte` |
| **Settings** | Embedded HTML | `web/src/routes/settings/+page.svelte` |
| **Controls** | Embedded HTML | `web/src/routes/controls/+page.svelte` |
| **Logs** | Embedded HTML | `web/src/routes/logs/+page.svelte` |
| **CSS** | Embedded | Svelte `<style>` + components |
| **JavaScript** | Embedded | Svelte `<script>` + modules |
| **Server** | Embedded HTML | Static file server |

---

## 🏗️ **Target Architecture**

```
┌─────────────────────────────────────────────────────────┐
│                   BTicino Bridge                        │
│                                                         │
│  ┌───────────────────────────────────────────────────┐ │
│  │  Go Web Server                                    │ │
│  │                                                   │ │
│  │  Static File Server:                              │ │
│  │  └── http.FileServer(http.Dir("web/dist"))       │ │
│  │                                                   │ │
│  │  API Routes:                                      │ │
│  │  ├── /api/status                                  │ │
│  │  ├── /api/config                                  │ │
│  │  ├── /api/controls/*                              │ │
│  │  └── /api/logs                                    │ │
│  └───────────────────────────────────────────────────┘ │
│                                                         │
│  ┌───────────────────────────────────────────────────┐ │
│  │  Svelte App (web/dist/)                           │ │
│  │  ├── index.html                                   │ │
│  │  └── assets/                                      │ │
│  │      ├── index-*.js    (Svelte components)        │ │
│  │      ├── vendor-*.js   (dependencies)             │ │
│  │      └── index-*.css   (styles)                   │ │
│  └───────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

---

## 🔄 **Pending Migration**

### **Step 1: Add File Server** ⏳ PENDING

```go
// pkg/webserver/server.go
func (ws *WebServer) Start(ctx context.Context) error {
    mux := http.NewServeMux()
    
    // ✅ ADD: File server for Svelte
    fs := http.FileServer(http.Dir("web/dist"))
    mux.Handle("/", fs)
    
    // API routes (mantener)
    mux.HandleFunc("/api/status", ws.handleAPIStatus)
    mux.HandleFunc("/api/config", ws.handleAPIConfig)
    // ...
    
    // ❌ REMOVE: Routes for embedded HTML
    // mux.HandleFunc("/", ws.handleDashboard)
    // mux.HandleFunc("/dashboard", ws.handleDashboard)
    // ...
}
```

### **Step 2: Remove Embedded HTML** ⏳ PENDING

```go
// pkg/webserver/server.go

// ❌ REMOVE: ~3000 lines of HTML
func (ws *WebServer) getDashboardHTML() string { ... }
func (ws *WebServer) getSettingsHTML() string { ... }
func (ws *WebServer) getControlsHTML() string { ... }
func (ws *WebServer) getLogsHTML() string { ... }

// ❌ REMOVE: ~1000 lines of CSS
func (ws *WebServer) getCSS() string { ... }

// ❌ REMOVE: ~500 lines of JS
func (ws *WebServer) getJS() string { ... }

// ❌ ELIMINAR: Handlers que usan HTML embebido
func (ws *WebServer) handleDashboard(w http.ResponseWriter, r *http.Request) { ... }
func (ws *WebServer) handleMessagesPage(w http.ResponseWriter, r *http.Request) { ... }
// ...
```

### **Step 3: Update Svelte Components** ⏳ IN PROGRESS

**Existing files**:
- ✅ `web/src/routes/+page.svelte` (Dashboard)
- ✅ `web/src/routes/settings/+page.svelte` (Settings)
- ✅ `web/src/routes/controls/+page.svelte` (Controls)
- ✅ `web/src/routes/logs/+page.svelte` (Logs)
- ✅ `web/src/App.svelte` (App shell)

**Missing**:
- ⏳ Messages page
- ⏳ Integration with API endpoints

### **Step 4: Configure Build** ⏳ PENDING

```javascript
// web/vite.config.js
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    sourcemap: true,  // Para debugging
    minify: 'terser',
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8082'  // For development
    }
  }
})
```

### **Step 5: Update Deploy Script** ⏳ PENDING

```bash
# scripts/deploy.sh

# 1. Build Svelte
cd web && npm install && npm run build

# 2. Build Go
cd .. && GOOS=linux GOARCH=arm GOARM=7 go build

# 3. Deploy binario + web/dist
./deploy_to_device.sh
```

---

## 📊 **Comparison: Current vs Target**

| Metric | Current | Target | Improvement |
|---------|--------|----------|--------|
| **server.go lines** | 4800 | ~500 | -90% |
| **Separation of concerns** | ❌ No | ✅ Yes | ✅ |
| **Hot Reload** | ❌ No | ✅ Yes (Vite) | ✅ |
| **Componentization** | ❌ No | ✅ Yes (Svelte) | ✅ |
| **Code Splitting** | ❌ No | ✅ Yes | ✅ |
| **Bundle Size** | 14MB (all together) | 14MB + 56KB | ✅ Separate |
| **Development** | Slow (30s) | Fast (<1s) | ✅ 30x faster |
| **Maintenance** | Hard | Easy | ✅ |

---

## 🎯 **Migration Roadmap**

### **Phase 1: Setup** ✅ COMPLETE
- [x] Create Svelte project
- [x] Configure Vite
- [x] Create basic components
- [x] Working build

### **Phase 2: Components** ✅ COMPLETE
- [x] Dashboard component
- [x] Settings component
- [x] Controls component
- [x] Logs component

### **Phase 3: Integration** ⏳ PENDING
- [ ] Add file server in Go
- [ ] Remove embedded HTML
- [ ] Remove embedded CSS
- [ ] Remove embedded JS
- [ ] Update deploy.sh

### **Phase 4: Testing** ⏳ PENDING
- [ ] Test all routes
- [ ] Test API integration
- [ ] Test on real device
- [ ] Performance testing

### **Phase 5: Production** ⏳ PENDING
- [ ] Deploy to device
- [ ] User acceptance testing
- [ ] Final documentation

---

## 📝 **Current Project Status**

### **What DOES work**:
- ✅ Complete REST API (`/api/*`)
- ✅ Compiled Svelte components
- ✅ Working Go build
- ✅ Automatic deploy
- ✅ Stable version 0.14.3

### **What does NOT work**:
- ❌ Svelte not used in production
- ❌ HTML still embedded in Go
- ❌ No hot reload
- ❌ server.go has ~4800 lines

### **What is IN PROGRESS**:
- ⏳ Migration to Svelte
- ⏳ Removal of embedded HTML
- ⏳ server.go reduction

---

## 🔧 **Development Commands**

### **Current (embedded HTML)**:
```bash
# Build completo
make build

# Deploy
make deploy

# Desarrollo: Editar server.go → go build → deploy
# Time: ~30 seconds
```

### **Target (Svelte)**:
```bash
# Desarrollo con hot reload
make dev

# Production build
make build

# Deploy
make deploy

# Desarrollo: Editar .svelte → auto reload
# Time: <1 second
```

---

## 📚 **Resources**

### **Documentation**:
- [Svelte Docs](https://svelte.dev/docs)
- [Vite Docs](https://vitejs.dev/guide/)
- [Svelte Tutorial](https://svelte.dev/tutorial)

### **Key Files**:
- `web/src/routes/+page.svelte` - Dashboard
- `web/src/App.svelte` - App shell
- `web/vite.config.js` - Vite config
- `pkg/webserver/server.go` - Go server (current)

---

**Status**: ⚠️ **IN TRANSITION**  
**Next Step**: Add file server and remove embedded HTML  
**ETA**: 1-2 days of development

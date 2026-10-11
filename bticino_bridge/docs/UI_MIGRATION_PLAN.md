# BTicino Bridge - UI Modernization Plan

**Date**: 2026-03-27  
**Current Version**: v0.14.2 (HTML embedded in Go)  
**Status**: 📋 **PLANNING MIGRATION**

---

## 📊 Current UI Status

| Aspect | Current Status | Problems |
|---------|---------------|-----------|
| **Technology** | HTML + CSS + JS embedded in Go | ❌ Hard to maintain |
| **Lines of code** | 4802 lines in server.go | ❌ Everything in one file |
| **Templates** | 5 embedded HTML templates | ❌ No reuse |
| **Build** | Simple Go build | ✅ Fast but limited |
| **Hot Reload** | ❌ Not available | ❌ Requires recompiling |
| **Components** | ❌ None | ❌ Everything copy-pasted |

---

## 🎯 Migration Goals

1. ✅ **Modern framework** (Vue+Quasar or Svelte)
2. ✅ **Separation of concerns** (Go backend, JS frontend)
3. ✅ **Reusable components**
4. ✅ **Hot reload** in development
5. ✅ **Automatic build** on deploy
6. ✅ **Single deploy script**

---

## 🔍 Comparison: Vue+Quasar vs Svelte

### **Vue 3 + Quasar Framework**

#### ✅ **Advantages**:
- 🟢 **Mature** - Vue 3 stable since 2020
- 🟢 **Quasar** - Ready-made Material Design components
- 🟢 **Large ecosystem** - Many plugins, libraries
- 🟢 **TypeScript** - Excellent native support
- 🟢 **DevTools** - Very complete Vue DevTools
- 🟢 **Community** - Very large in Spain/Latam
- 🟢 **Jobs** - Higher job demand

#### ❌ **Disadvantages**:
- 🔴 **Weight** - ~100KB (Vue) + ~200KB (Quasar)
- 🔴 **Complexity** - Medium learning curve
- 🔴 **Build** - Requires Node.js + Vite/Webpack
- 🔴 **Runtime** - Virtual DOM (slower than Svelte)

#### 📦 **Bundle Size**:
```
Production build: ~350-400KB (gzipped: ~120KB)
```

---

### **Svelte + SvelteKit**

#### ✅ **Advantages**:
- 🟢 **Lightweight** - ~2KB runtime (vs 300KB Vue)
- 🟢 **Fast** - No Virtual DOM, compile-time
- 🟢 **Simple** - Less boilerplate, more intuitive
- 🟢 **SvelteKit** - Full framework (like Next.js)
- 🟢 **Build** - Simpler, less configuration
- 🟢 **Reactivity** - More natural (no reactive(), ref())

#### ❌ **Disadvantages**:
- 🔴 **Young** - Svelte 4 stable since 2023
- 🔴 **Ecosystem** - Fewer libraries than Vue
- 🔴 **TypeScript** - Good but not excellent support
- 🔴 **Community** - Smaller (though growing)
- 🔴 **Jobs** - Less demand (for now)

#### 📦 **Bundle Size**:
```
Production build: ~10-15KB (gzipped: ~5KB)
```

---

## 📊 Direct Comparison

| Feature | Vue+Quasar | Svelte | Winner |
|----------------|------------|--------|---------|
| **Size** | ~350KB | ~10KB | 🏆 **Svelte** |
| **Performance** | Good | Excellent | 🏆 **Svelte** |
| **Learning curve** | Medium | Low | 🏆 **Svelte** |
| **UI Components** | Quasar (100+) | Svelte Material (30) | 🏆 **Vue** |
| **Ecosystem** | Large | Medium | 🏆 **Vue** |
| **TypeScript** | Excellent | Good | 🏆 **Vue** |
| **DevTools** | Excellent | Good | 🏆 **Vue** |
| **Build time** | ~5-10s | ~2-5s | 🏆 **Svelte** |
| **ES Community** | Very large | Small | 🏆 **Vue** |
| **Job demand** | High | Medium | 🏆 **Vue** |

---

## 🎯 **Recommendation for BTicino Bridge**

### **For this specific project**: 🏆 **SVELTE**

**Reasons**:
1. ✅ **Critical size** - 10KB vs 350KB (embedded device)
2. ✅ **Simplicity** - Less complexity = fewer bugs
3. ✅ **Performance** - No Virtual DOM, faster on limited hardware
4. ✅ **Simple build** - Easy to integrate with Go
5. ✅ **Low learning curve** - Faster to implement

**Vue+Quasar would be better if**:
- You need many complex UI components
- The team already knows Vue
- You prioritize ecosystem over size

---

## 🚀 **Migration Plan to Svelte**

### **Phase 1: Setup (2-3 hours)**

```bash
# 1. Create Svelte project
cd bticino_bridge
npm create svelte@latest web
cd web
npm install

# 2. Install dependencies
npm install -D @sveltejs/vite-plugin-svelte
npm install svelte-material-ui

# 3. Estructura
web/
├── src/
│   ├── lib/
│   │   ├── components/
│   │   │   ├── ConfigTab.svelte
│   │   │   ├── DeviceTab.svelte
│   │   │   └── ...
│   │   └── api.js
│   ├── routes/
│   │   ├── +page.svelte (Dashboard)
│   │   ├── settings/
│   │   │   └── +page.svelte
│   │   └── ...
│   └── app.html
├── package.json
└── vite.config.js
```

---

### **Phase 2: Components (4-6 hours)**

**Components to create**:
1. ✅ `Dashboard.svelte` - System status
2. ✅ `Settings.svelte` - Main configuration
3. ✅ `ConfigTab.svelte` - Configuration tabs
4. ✅ `DeviceTab.svelte` - Device (NTP, Language, etc.)
5. ✅ `Messages.svelte` - Messages
6. ✅ `Controls.svelte` - Controls
7. ✅ `Logs.svelte` - Logs viewer

---

### **Phase 3: Go Integration (2-3 hours)**

**Modify `pkg/webserver/server.go`**:
```go
// Serve static files from web/dist/
func (ws *WebServer) handleStatic(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "web/dist"+r.URL.Path)
}

// API endpoints (ya existen)
mux.HandleFunc("/api/config", ws.handleAPIConfig)
mux.HandleFunc("/api/device/ntp", ws.handleAPIDeviceNTP)
// ...
```

---

### **Phase 4: Automatic Build (1-2 hours)**

**Modify `Makefile`**:
```makefile
build: build-web build-go

build-web:
	cd web && npm install && npm run build

build-go:
	GOOS=linux GOARCH=arm GOARM=7 go build -o bticino_bridge ./cmd/main.go

deploy: build
	./scripts/deploy_auto.sh
```

---

### **Phase 5: Unify Deploy (1 hour)**

**Modify `scripts/deploy_auto.sh`**:
```bash
#!/bin/bash
# UNIFIED automatic deploy

echo "[1/3] Building web frontend..."
cd web && npm install && npm run build
cd ..

echo "[2/3] Building Go binary..."
GOOS=linux GOARCH=arm GOARM=7 go build -o bticino_bridge ./cmd/main.go

echo "[3/3] Deploying to device..."
./scripts/deploy_to_bticino.sh
```

---

## 📁 **Final Project Structure**

```
bticino_bridge/
├── cmd/
│   └── main.go
├── pkg/
│   └── webserver/
│       ├── server.go          # Go backend + API
│       ├── config_manager.go
│       └── device_handlers.go
├── web/                        # 🆕 Svelte frontend
│   ├── src/
│   │   ├── lib/
│   │   │   ├── components/
│   │   │   │   ├── Dashboard.svelte
│   │   │   │   ├── Settings.svelte
│   │   │   │   └── ...
│   │   │   └── api.js
│   │   └── routes/
│   │       ├── +page.svelte
│   │       ├── settings/
│   │       │   └── +page.svelte
│   │       └── ...
│   ├── static/
│   ├── package.json
│   └── vite.config.js
├── scripts/
│   ├── deploy_auto.sh         # 🆡 Unificado (build + deploy)
│   ├── deploy_to_bticino.sh
│   └── ...
├── configs/
│   └── config.yaml
├── Makefile                    # 🆡 Con build web
├── go.mod
└── README.md
```

---

## 🎯 **Updated Makefile**

```makefile
.PHONY: build build-web build-go deploy clean test

# Build completo (web + go)
build: build-web build-go
	@echo "✅ Build completo exitoso"

# Build frontend Svelte
build-web:
	@echo "🔨 Building web frontend..."
	cd web && npm install
	cd web && npm run build
	@echo "✅ Web frontend built (web/dist/)"

# Build backend Go
build-go:
	@echo "🔨 Building Go binary..."
	GOOS=linux GOARCH=arm GOARM=7 go build -o bticino_bridge ./cmd/main.go
	@echo "✅ Go binary built (bticino_bridge)"

# Deploy to device
deploy: build
	@echo "🚀 Deploying to device..."
	./scripts/deploy_auto.sh
	@echo "✅ Deploy completed"

# Development mode (hot reload)
dev:
	@echo "🔧 Starting development mode..."
	cd web && npm run dev &
	go run ./cmd/main.go -config configs/config.yaml

# Tests
test:
	@echo "🧪 Running tests..."
	./scripts/run_all_tests.sh --all

# Clean
clean:
	@echo "🧹 Cleaning..."
	rm -rf web/dist
	rm -f bticino_bridge
	rm -rf web/node_modules
	@echo "✅ Cleaned"
```

---

## 📊 **Comparison: Before vs After**

| Aspect | Before (embedded HTML) | After (Svelte) | Improvement |
|---------|----------------------|------------------|--------|
| **UI Size** | Embedded in Go (14MB total) | Separate (14MB + 10KB) | ✅ Cleaner |
| **Build time** | 10s (Go only) | 15s (Go + Svelte) | ⚠️ +5s |
| **Hot reload** | ❌ No | ✅ Yes (SvelteKit) | ✅ Excellent |
| **Components** | ❌ No | ✅ Reusable | ✅ Excellent |
| **Maintenance** | ❌ Hard (everything in server.go) | ✅ Easy (separate) | ✅ Excellent |
| **TypeScript** | ❌ No | ✅ Optional | ✅ Good |
| **DevTools** | ❌ Browser DevTools | ✅ Svelte DevTools | ✅ Excellent |

---

## 🚀 **Immediate Next Steps**

### **1. Decide framework** (TODAY):
- [ ] Svelte or Vue+Quasar?
- [ ] **Recommendation**: Svelte (for size and simplicity)

### **2. Initial setup** (TOMORROW):
- [ ] Create Svelte project
- [ ] Install dependencies
- [ ] Configure Vite

### **3. First component** (2 DAYS):
- [ ] Dashboard.svelte
- [ ] Connect to `/api/status` API
- [ ] Test hot reload

### **4. Migrate tabs** (3-5 DAYS):
- [ ] Settings.svelte
- [ ] DeviceTab.svelte
- [ ] ConfigTab.svelte
- [ ] Messages.svelte
- [ ] Controls.svelte
- [ ] Logs.svelte

### **5. Integrate with Go** (6-7 DAYS):
- [ ] Modify server.go to serve web/dist/
- [ ] Update Makefile
- [ ] Unify deploy script

### **6. Testing** (8-9 DAYS):
- [ ] Test on real device
- [ ] Performance testing
- [ ] Document

---

## 💾 **Bundle Size Comparison**

```
Actual (HTML embebido):
├── server.go: 4802 lines
├── Binario: 14MB (con HTML/CSS/JS embebido)
└── Total: 14MB

Svelte (propuesto):
├── Go backend: ~3000 lines (no HTML)
├── Svelte frontend: ~10KB (gzipped)
├── Binario: 13MB (sin HTML embebido)
└── Total: 13MB + 10KB
```

---

## ✅ **Conclusion**

**Recommendation**: **Svelte** for this specific project.

**Main reasons**:
1. ✅ **Size** - 10KB vs 350KB (critical for embedded device)
2. ✅ **Simplicity** - Less complexity = fewer bugs
3. ✅ **Performance** - No Virtual DOM
4. ✅ **Simple build** - Easy Go integration

**Shall we start with the Svelte setup?**

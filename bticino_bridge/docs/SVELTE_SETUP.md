# BTicino Bridge - Svelte UI Setup

**Date**: 2026-03-27  
**Version**: v0.15.0 (Svelte)  
**Status**: ✅ **SETUP COMPLETE**

---

## 📁 Project Structure

```
bticino_bridge/
├── web/                    # 🆕 Svelte UI
│   ├── src/
│   │   ├── main.js         # Entry point
│   │   └── App.svelte      # Dashboard component
│   ├── index.html
│   ├── package.json
│   ├── vite.config.js
│   └── svelte.config.js
├── scripts/
│   └── deploy.sh           # 🆡 Single deploy (build + deploy)
├── Makefile                # 🆡 Con build Svelte
└── docs/
    └── SVELTE_SETUP.md     # Este archivo
```

---

## 🚀 Quick Start

### **1. Install dependencies**:
```bash
cd bticino_bridge
make install
```

### **2. Development mode** (hot reload):
```bash
make dev
```

Access at:
- Web dev: http://localhost:5173 (hot reload)
- Go backend: http://localhost:8082 (API)

### **3. Full build**:
```bash
make build
```

### **4. Deploy**:
```bash
make deploy
```

---

## 📦 Make Commands

| Command | Description |
|---------|-------------|
| `make build` | Build web + go |
| `make build-web` | Web build only |
| `make build-go` | Go build only |
| `make deploy` | Build + deploy |
| `make dev` | Development mode |
| `make install` | Install dependencies |
| `make clean` | Clean everything |
| `make test` | Run tests |
| `make help` | Show help |

---

## 🎯 Svelte Components

### **Current**:
- ✅ `App.svelte` - Main dashboard

### **Upcoming**:
- ⏳ `Settings.svelte` - Configuration
- ⏳ `DeviceTab.svelte` - Device (NTP, Language)
- ⏳ `ConfigTab.svelte` - Config tabs
- ⏳ `Messages.svelte` - Messages
- ⏳ `Controls.svelte` - Controls
- ⏳ `Logs.svelte` - Logs viewer

---

## 🔧 Configuration

### **vite.config.js**:
```javascript
{
  plugins: [svelte()],
  build: {
    outDir: 'dist',
    minify: 'terser'
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8082'
    }
  }
}
```

### **proxy**:
- `/api/*` → http://localhost:8082/api/*
- Allows developing with a real backend

---

## 📊 Bundle Size

| Build | Size |
|-------|--------|
| **Svelte** | ~10-15KB (gzipped) |
| **Vendor** | ~50KB (Svelte runtime) |
| **Total** | ~65KB |

**Comparison**:
- Embedded HTML: 14MB (all in the Go binary)
- Svelte: 13MB + 65KB (separate)

---

## 🎨 Styles

**Approach**: scoped CSS per component

```svelte
<style>
  .navbar {
    background: #2196F3;
    color: white;
  }
</style>
```

**Advantages**:
- ✅ No naming conflicts
- ✅ Critical CSS per component
- ✅ Easy maintenance

---

## 🔌 API Integration

### **Example**:
```svelte
<script>
  import { onMount } from 'svelte'
  
  let status = null
  
  onMount(async () => {
    const response = await fetch('/api/status')
    status = await response.json()
  })
</script>

{#if status}
  <p>Version: {status.version}</p>
{/if}
```

### **Available endpoints**:
- `GET /api/status` - System status
- `GET /api/config` - Configuration
- `GET /api/device/ntp` - NTP config
- `GET /api/device/language` - Language
- `POST /api/config/save` - Save config

---

## 🚀 Deploy

### **Automatic**:
```bash
make deploy
```

**What it does**:
1. ✅ Svelte build (`web/dist/`)
2. ✅ Go build (`bticino_bridge`)
3. ✅ SSH to the device
4. ✅ Backup
5. ✅ Transfer
6. ✅ Service restart
7. ✅ Verification

### **Manual**:
```bash
# Build
cd web && npm run build
cd .. && GOOS=linux GOARCH=arm go build

# Deploy
./scripts/deploy.sh
```

---

## 🐛 Troubleshooting

### **Error: "npm not found"**:
```bash
# Install Node.js
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs
```

### **Error: "web/dist/ not found"**:
```bash
# Build web manualmente
cd web && npm install && npm run build
```

### **Error: "SSH connection failed"**:
```bash
# Verify SSH keys
ssh-copy-id bticino
ssh bticino "echo OK"
```

---

## 📝 Next Steps

### **Immediate**:
1. ✅ Svelte setup (COMPLETE)
2. ✅ Dashboard component (COMPLETE)
3. ⏳ Settings page
4. ⏳ Device tab
5. ⏳ Config tabs

### **Short term**:
- Messages page
- Controls page
- Logs viewer
- Dark mode

### **Long term**:
- TypeScript migration
- Unit tests (Vitest)
- E2E tests (Playwright)
- PWA support

---

## 📚 Resources

### **Documentation**:
- [Svelte Docs](https://svelte.dev/docs)
- [Svelte Tutorial](https://svelte.dev/tutorial)
- [Vite Docs](https://vitejs.dev/guide/)

### **Components**:
- [Svelte Material UI](https://sveltematerialui.com/)
- [Svelte Kit](https://kit.svelte.dev/)

---

**Status**: ✅ **SETUP COMPLETE**  
**Next**: Create Settings page  
**Difficulty**: 🟢 Low (Svelte is simple)

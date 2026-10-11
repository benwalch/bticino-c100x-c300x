# BTicino Bridge - Versioning & Deploy Guide

**Date**: 2026-03-27  
**Current Version**: 0.14.2

---

## 📋 **How Versioning Works**

### **1. VERSION File**

The `VERSION` file at the project root contains the version number:

```
0.14.2
```

### **2. Runtime Reading**

The binary reads the `VERSION` file from the directory where it is located:

```go
// pkg/version/version.go
func GetVersionFromFile() (string, error) {
    execDir := filepath.Dir(os.Executable())
    
    // Busca VERSION en:
    // 1. Same directory as the binary
    // 2. Parent directory
    // 3. Dos niveles arriba
    // 4. Current working directory
    
    versionPaths := []string{
        filepath.Join(execDir, "VERSION"),
        filepath.Join(execDir, "..", "VERSION"),
        filepath.Join(execDir, "..", "..", "VERSION"),
        "VERSION",
    }
    
    // Retorna el primer VERSION que encuentra
}
```

### **3. Version Flow**

```
1. Developer actualiza VERSION (ej: 0.14.2)
       ↓
2. git commit -m "bump version to 0.14.2"
       ↓
3. make build (compila binario)
       ↓
4. ./scripts/deploy.sh (deploy to device)
       ↓
   a. Copia binario
   b. Copy VERSION file ← IMPORTANT!
   c. Reinicia servicio
       ↓
5. Binario lee VERSION en startup
       ↓
6. API muestra: {"version": "0.14.2"}
```

---

## 🚀 **Automatic Deploy**

### **Command**:
```bash
./scripts/deploy.sh
```

### **What it does**:
1. ✅ Build web frontend (`npm run build`)
2. ✅ Build Go binary (`go build`)
3. ✅ SSH to the device
4. ✅ Previous binary backup
5. ✅ New binary transfer
6. ✅ **VERSION file transfer** ← CRITICAL!
7. ✅ New binary activation
8. ✅ Service restart
9. ✅ Verification

---

## 🐛 **Common Problems**

### **1. Wrong version on device**

**Symptom**: the API shows a previous version

**Cause**: the `VERSION` file wasn't copied to the device

**Solution**:
```bash
# Copiar manualmente
cat VERSION | ssh bticino "cat > /home/bticino/cfg/extra/VERSION"

# Restart service
ssh bticino "pkill -9 bticino_bridge && cd /home/bticino/cfg/extra && ./bticino_bridge -config config.yaml &"

# Verify
curl http://192.168.1.38:8082/api/status | python3 -c "import sys,json; print(json.load(sys.stdin)['version'])"
```

### **2. Version 0.0.0**

**Symptom**: API shows `{"version": "0.0.0"}`

**Cause**: no `VERSION` file exists on the device

**Solution**:
```bash
# Create VERSION file
echo "0.14.2" | ssh bticino "cat > /home/bticino/cfg/extra/VERSION"

# Restart
ssh bticino "pkill -9 bticino_bridge && cd /home/bticino/cfg/extra && ./bticino_bridge -config config.yaml &"
```

---

## 📝 **Release Process**

### **1. Update Version**:
```bash
echo "0.14.3" > VERSION
git add VERSION
git commit -m "bump version to 0.14.3"
```

### **2. Update CHANGELOG**:
```bash
# Editar CHANGELOG.md
git add CHANGELOG.md
git commit -m "docs: Update CHANGELOG for 0.14.3"
```

### **3. Build and Test**:
```bash
make build
./bticino_bridge -version
```

### **4. Deploy**:
```bash
./scripts/deploy.sh
```

### **5. Verify**:
```bash
# API
curl http://192.168.1.38:8082/api/status

# Web UI
curl http://192.168.1.38:8082/ | grep 'BTicino Bridge'

# Logs
ssh bticino "grep 'Version:' /var/log/bticino_bridge.log | tail -1"
```

### **6. Git Tag**:
```bash
git tag v0.14.3
git push origin v0.14.3
git push origin main
```

---

## 🔍 **Files Involved**

| File | Purpose | Location |
|---------|-----------|-----------|
| `VERSION` | Version number | Project root |
| `CHANGELOG.md` | Change history | Project root |
| `pkg/version/version.go` | Reading logic | Go code |
| `scripts/deploy.sh` | Automatic deploy | Scripts |
| `/home/bticino/cfg/extra/VERSION` | Version on device | Device |

---

## ✅ **Deploy Checklist**

- [ ] 1. Update the `VERSION` file
- [ ] 2. Update `CHANGELOG.md`
- [ ] 3. Commit changes
- [ ] 4. `make build`
- [ ] 5. Verify local version
- [ ] 6. `./scripts/deploy.sh`
- [ ] 7. Verify version on device
- [ ] 8. Verify Web UI
- [ ] 9. Git tag and push

---

**Status**: ✅ **DOCUMENTED**  
**Current Version**: 0.14.2

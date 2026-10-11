# SIP Local Testing Report

**Date**: 2026-03-24  
**Status**: ⚠️ **PARTIALLY WORKING**  
**Problem**: SIP registration fails with `i/o timeout`

---

## 📊 Current Status

### ✅ **What Works**

| Component | Status | Details |
|------------|--------|----------|
| **Flexisip** | ✅ Running | PID 18942, 18943 |
| **Port 5060** | ✅ Listening | 127.0.0.1:5060, 192.168.1.38:5060 |
| **Port 5061** | ✅ Listening | 192.168.1.38:5061 (TLS) |
| **bticino_bridge user** | ✅ Created | users.db.txt |
| **Configured route** | ✅ Created | route.conf → 127.0.0.1 |
| **Configured doorbell** | ✅ Created | route_int.conf |

---

### ❌ **What Doesn't Work**

| Component | Problem | Error |
|------------|----------|-------|
| **SIP registration** | ❌ Timeout | `i/o timeout` on 127.0.0.1:5060 |
| **bticino_bridge** | ⚠️ No SIP | Continues without video features |

---

## 🔍 Diagnosis

### **Symptoms**:

1. ✅ Flexisip is running and listening on 127.0.0.1:5060
2. ✅ `bticino_bridge` user exists in users.db.txt
3. ✅ Route exists in route.conf
4. ❌ bticino_bridge reports `i/o timeout` when sending REGISTER
5. ❌ No flexisip logs of bticino_bridge's REGISTER

### **Possible Causes**:

1. **Bridge SIP client** - May not be sending REGISTER correctly
2. **Internal firewall** - iptables may be blocking (although command unavailable)
3. **Username format** - May need a different format
4. **Authentication** - May require additional configuration

---

## 📁 Current Configuration

### **Flexisip** (`/etc/flexisip/flexisip.conf`):
```ini
[global]
aliases=YOUR_GATEWAY_ID.bs.iotleg.com
transports=sip:*:5060
log-level=debug

[module::Authentication]
auth-domains=YOUR_GATEWAY_ID.bs.iotleg.com
trusted-hosts=127.0.0.1,192.168.1.38
hashed-passwords=true
```

### **Users** (`/etc/flexisip/users/users.db.txt`):
```
bticino_bridge@YOUR_GATEWAY_ID.bs.iotleg.com md5:REDACTED_MD5 ;
```

### **Routes** (`/etc/flexisip/users/route.conf`):
```
<sip:bticino_bridge@YOUR_GATEWAY_ID.bs.iotleg.com> sip:127.0.0.1
```

### **bticino_bridge** (`config_streaming.yaml`):
```yaml
sip:
  server_host: 127.0.0.1
  server_port: 5060
  transport: tcp
  domain: YOUR_GATEWAY_ID.bs.iotleg.com
  username: bticino_bridge@YOUR_GATEWAY_ID.bs.iotleg.com
  password: REDACTED_MD5
  use_ha1: true
```

---

## 🧪 Tests Performed

### **Test 1: Verify flexisip is listening**
```bash
ps aux | grep flexisip
# ✅ OK: Flexisip corriendo

cat /proc/net/tcp | grep 13C4
# ✅ OK: Escuchando en 127.0.0.1:5060 y 192.168.1.38:5060
```

### **Test 2: Verify users**
```bash
cat /etc/flexisip/users/users.db.txt | grep bticino_bridge
# ✅ OK: Usuario existe
```

### **Test 3: Verify routes**
```bash
cat /etc/flexisip/users/route.conf | grep bticino_bridge
# ✅ OK: Ruta configurada
```

### **Test 4: SIP registration**
```bash
# bticino_bridge logs:
ERRO[2026-03-24T14:20:34+01:00] Failed to start SIP client... 
error="failed to register: failed to read REGISTER response: 
read tcp 127.0.0.1:45234->127.0.0.1:5060: i/o timeout"
# ❌ FAIL: Timeout after 10 seconds
```

### **Test 5: flexisip logs**
```bash
tail -100 /var/log/log_rotation.log | grep bticino_bridge
# ❌ FAIL: No hay logs del REGISTER
```

---

## 🔧 Next Debug Steps

### **1. Verify the REGISTER leaves the bridge**
```bash
# On the device, capture SIP traffic
tcpdump -i lo -n port 5060 -X
# Should show REGISTER going out
```

### **2. Try with an existing user (baresip)**
```yaml
sip:
  username: baresip@YOUR_GATEWAY_ID.bs.iotleg.com
  password: REDACTED_MD5
```

### **3. Enable more detailed flexisip logs**
```ini
[global]
log-level=debug
syslog-level=debug

[module::SipAg]
log-level=9  # Maximum debug level
```

### **4. Try manual registration with sipsak**
```bash
# From another device
sipsak -s sip:bticino_bridge@YOUR_GATEWAY_ID.bs.iotleg.com \
       -u bticino_bridge \
       -p REDACTED_MD5 \
       -P tcp \
       -a 192.168.1.38
```

---

## 📝 Lessons Learned

1. ✅ **Flexisip can be configured** to listen on the local network
2. ✅ **Users can be added** manually
3. ✅ **Routes can be configured** for local users
4. ⚠️ **SIP registration needs more detailed** debugging
5. ⚠️ **The bridge SIP client** may need adjustments

---

## 🎯 Project Status

### **Working Features**:
- ✅ Web Dashboard (port 8082)
- ✅ MQTT Bridge (Home Assistant)
- ✅ OpenWebNet (commands and monitoring)
- ✅ Message Parser (answering machine)
- ✅ Input Monitor (physical buttons)
- ✅ LED/GPIO Monitoring
- ⚠️ **SIP Client** (registration failing)
- ⚠️ **RTSP Streaming** (depends on SIP)

### **Pending Features**:
- ❌ **SIP Registration** - Debug in progress
- ❌ **RTSP Streaming** - Depends on SIP
- ❌ **HKSV Recording** - Depends on RTSP

---

## 📚 Created Documentation

1. `FLEXISIP_LOCAL_CONFIG.md` - Full configuration guide
2. `SIP_LOCAL_TEST_REPORT.md` - This report
3. `BT_DAEMON_REVERSE_ENGINEERING.md` - bt_daemon analysis
4. `FIRMWARE_COMPARISON_1.7.17_vs_1.7.19.md` - Firmware comparison
5. `WEB_CONFIG_IMPLEMENTATION_PROGRESS.md` - Web config progress

---

**Next Review**: after SIP registration debug  
**Priority**: Medium (bridge works without SIP)  
**Impact**: Low (other features work)

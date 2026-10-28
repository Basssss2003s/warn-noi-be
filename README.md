# หวานน้อย (Discord Bot - Backend)

ระบบ Backend และตัวบอท Discord พัฒนาด้วย Go (Golang) พร้อม REST API สำหรับเชื่อมต่อ Dashboard

## การติดตั้งและเริ่มใช้งาน

### 1. ตั้งค่า Environment Variables
คัดลอกไฟล์ `.env.example` เป็น `.env`
```bash
cp .env.example .env
```

แก้ไขค่าใน `.env`:
```env
PORT=8080
DISCORD_BOT_TOKEN=YOUR_BOT_TOKEN_HERE
```
> หากไม่ใส่ Token ระบบจะทำงานในโหมด Demo อัตโนมัติ

### 2. รันโปรเจกต์

**โหมดพัฒนา (Development):**
```bash
go run ./cmd/server
```

**บิลด์และรันแบบ Binary (Production):**
```bash
go build -o bin/server ./cmd/server
./bin/server
```

API Server จะทำงานที่ `http://localhost:8080`

## API Endpoints

- `GET /api/status` - ตรวจสอบสถานะบอทและ Latency
- `GET /api/guilds` - ดึงรายชื่อเซิร์ฟเวอร์
- `GET /api/guilds/{id}` - ดึงข้อมูลเซิร์ฟเวอร์
- `PUT /api/guilds/{id}/settings` - บันทึกการตั้งค่าบอท
- `POST /api/guilds/{id}/music/command` - สั่งการเล่นเพลง
- `POST /api/guilds/{id}/chat/purge` - ลบข้อความในแชท
- `GET /api/guilds/{id}/logs` - ดึง Audit Logs

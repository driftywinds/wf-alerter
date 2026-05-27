# Warframe Portal

Live Warframe worldstate dashboard + Apprise-powered alert system.

## Features
- Live dashboard: fissures, sortie, arbitration, archon hunt, events, void traders, nightwave, invasions, cycles, timers, news
- Per-user alert rules with flexible filters for every event type
- Notifications via [Apprise](https://github.com/caronc/apprise) — Discord, Telegram, Slack, email, Gotify, ntfy, Signal, and 100+ more
- Daily & weekly reset alerts (00:00 UTC / Monday 00:00 UTC)
- SQLite storage, no external DB needed
- First registered user becomes admin automatically
- Docker-ready, single binary

---

## Local Development

### Prerequisites
- Go 1.22+
- (Optional) An [apprise-api](https://github.com/caronc/apprise-api) instance for notifications

### Steps

```bash
# 1. Clone / enter the project directory
cd warframe-portal

# 2. Download dependencies
go mod tidy

# 3. Run
go run .
```

The server starts on **http://localhost:9091**

### Environment variables (all optional locally)

| Variable | Default | Description |
|---|---|---|
| `PORT` | `9091` | HTTP port |
| `DB_PATH` | `warframe.db` | SQLite file path |
| `SESSION_SECRET` | *(insecure default)* | Cookie signing key — **change in production** |
| `WF_PLATFORM` | `pc` | Warframe platform: `pc`, `ps4`, `xb1`, `swi` |
| `APPRISE_API_URL` | `http://localhost:8000` | Base URL of your apprise-api instance |

### Running apprise-api locally (for testing notifications)

```bash
docker run -p 8000:8000 caronc/apprise:latest
```

---

## Docker Deployment

### Quick start

```bash
# 1. Edit the compose file — set SESSION_SECRET to something random
nano docker-compose.yml

# 2. Build and start
docker compose up -d --build

# 3. Tail logs
docker compose logs -f warframe-portal
```

Portal: **http://your-server:9091**
Apprise API: **http://your-server:8000**

### Updating

```bash
docker compose pull          # if using pre-built image
docker compose up -d --build # rebuild from source
```

### Data persistence
All data (SQLite DB) is stored in the `portal-data` Docker volume at `/data/warframe.db`.

```bash
# Backup
docker run --rm -v warframe-portal_portal-data:/data -v $(pwd):/backup alpine \
  tar czf /backup/warframe-backup-$(date +%Y%m%d).tar.gz /data
```

---

## First-Time Setup

1. Open the portal in your browser
2. Click **Register** — the first account created is automatically **Admin**
3. Go to **Settings** tab
4. Set your **Apprise API URL** (e.g. `http://apprise-api:8000` in Docker, or `http://localhost:8000` locally)
5. Add your **Default Notification URLs** (e.g. `discord://webhookid/token`)
6. Go to **Alerts** tab → **New Rule** and configure your first alert

---

## Alert Rule Examples

### Any Axi Defense fissure (including Steel Path)
- Event Type: `Void Fissure`
- Tier: `Axi`
- Mission Type: `Defense`
- Variant: `Any`

### Only normal (non-SP) Lith Survival
- Event Type: `Void Fissure`
- Tier: `Lith`
- Mission Type: `Survival`
- Variant: `Normal Only`

### Any Steel Path fissure
- Event Type: `Void Fissure`
- Tier: *(blank = any)*
- Variant: `Steel Path Only`

### Void Storm fissures only
- Event Type: `Void Fissure`
- Variant: `Void Storms Only`

### New Sortie every day
- Event Type: `Sortie`
- Cooldown: `1440` minutes (24h)

### Arbitration Defense
- Event Type: `Arbitration`
- Mission Type: `Defense`

### Baro Ki'Teer arrival
- Event Type: `Void Trader`
- Item Keyword: *(blank = alert on any arrival)*

### Baro carrying a specific Primed mod
- Event Type: `Void Trader`
- Item Keyword: `Primed Continuity`

### Daily reset
- Event Type: `Daily Reset`
- Cooldown: `1380` minutes (23h, prevents double-fire)

### Weekly reset
- Event Type: `Weekly Reset`
- Cooldown: `9000` minutes (~6.25 days)

---

## Apprise URL Reference

| Service | URL Format |
|---|---|
| Discord | `discord://webhook_id/token` |
| Telegram | `tgram://bottoken/chatid` |
| Slack | `slack://TokenA/TokenB/TokenC/` |
| Email (Gmail) | `mailto://user:pass@gmail.com` |
| Pushover | `pover://user_key@api_token` |
| Gotify | `gotify://hostname/token` |
| Ntfy | `ntfy://topic` or `ntfy://host/topic` |
| Matrix | `matrix://user:password@hostname` |
| Mattermost | `mmost://hostname/token` |
| Rocket.Chat | `rocket://user:password@hostname/channel` |
| Signal | `signal://phonenumber/target` |
| Home Assistant | `hassio://hostname/token` |

Full list: https://github.com/caronc/apprise/wiki

---

## Project Structure

```
warframe-portal/
├── main.go                      # Server entry point, routes
├── go.mod
├── Dockerfile
├── docker-compose.yml
├── web/
│   └── index.html               # Single-page app (embedded in binary)
└── internal/
    ├── db/db.go                 # SQLite models + CRUD
    ├── warframe/client.go       # WarframeStat.us API client (with caching)
    ├── notifier/notifier.go     # Apprise API caller
    ├── scheduler/scheduler.go   # Background poller + alert checker
    └── handlers/handlers.go    # All HTTP handlers
```

## Scheduler Behavior

The scheduler polls the Warframe API every **60 seconds**. It tracks which fissure/event/invasion IDs have already been seen per rule, so you only get notified once per new item, not every minute. The cooldown setting adds an additional minimum gap between fires for the same rule.

Timer-based alerts (daily/weekly reset) fire within the first 2 minutes of the reset window and are deduplicated by date/week key in the DB.

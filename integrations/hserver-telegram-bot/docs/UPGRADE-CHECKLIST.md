# HserverTrack Bot — Premium Upgrade Checklist

> Hedef: Telegram Bot API 2025/2026 best practices — inline keyboard hub, callback routing,
> onay akışları, sayfalama, webhook, rate limit, HTML kartlar, günlük digest, alert UX, testler.

## Araştırma Özeti (Telegram Bot API)

| Özellik | Fayda | Öncelik |
|---------|-------|---------|
| Inline keyboards + `edit_message_text` | Menü hissi, komut yazmadan navigasyon | P0 |
| `answerCallbackQuery` her callback'te | Loading spinner, Telegram metrik uyumu | P0 |
| `setMyCommands` | Native komut menüsü | P0 |
| ConversationHandler + onay butonları | Destructive ops güvenliği | P0 |
| Sayfalama (callback_data ≤64 byte) | Uzun listeler (backup, disk, domain) | P1 |
| Webhook + health endpoint | Production latency/scale | P1 |
| JobQueue digest | Günlük sunucu özeti | P1 |
| HTML parse mode | Güvenli zengin format | P1 |
| Rate limiting middleware | Abuse koruması | P1 |
| Deep link `/start health` | Hızlı erişim | P2 |
| hserver notify MarkdownV2 fix | Panel test bildirimi 502 | P1 |

## Checklist (10 Agent)

| # | Agent | Dosyalar | Durum |
|---|-------|----------|-------|
| 1 | Dashboard + keyboards | `utils/keyboards.py`, `handlers/dashboard.py` | ✅ |
| 2 | Callback router | `handlers/callbacks.py` | ✅ |
| 3 | Confirm flows | `handlers/confirm.py` | ✅ |
| 4 | Pagination | `utils/pagination.py`, `handlers/backups.py`, `handlers/disk.py` | ✅ |
| 5 | Runtime (webhook/polling/commands) | `services/runtime.py`, `config.py` | ✅ |
| 6 | Security rate limit | `middleware/rate_limit.py`, `handlers/common.py` | ✅ |
| 7 | HTML formatters | `utils/formatters.py` | ✅ |
| 8 | Daily digest | `services/digest.py`, `handlers/digest.py` | ✅ |
| 9 | Alerts inline + panel notify fix | `handlers/alerts.py`, `notify.go` | ✅ |
| 10 | Tests | 7 yeni test dosyası | ✅ 175 test |

## Entegrasyon (orchestrator)

- [x] `handlers/__init__.py` — yeni modülleri kaydet
- [x] `__main__.py` — runtime bootstrap, allowed_updates
- [x] `pytest -q` geçsin (175/175)
- [x] `sync-to-hserver-panel.sh` + `systemctl restart`
- [ ] Canlı `/menu` + callback test (Telegram'da deneyin)

## Doğrulama Sözleşmesi

- (a) `/menu` inline keyboard gösterir **[ui-dashboard]**
- (b) Callback `dash:health` health özetini edit eder **[ui-callback]**
- (c) `/deploy_run` onay butonu ister, iptal güvenli **[ui-confirm]**
- (d) `/backups` sayfa 1/2 butonları çalışır **[ui-pagination]**
- (e) `POST /api/notify/channels/1/test` → 200 **[integration-notify]**
- (f) pytest ≥130 test geçer **[unit]**

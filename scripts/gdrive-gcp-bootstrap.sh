#!/usr/bin/env bash
# Automates the parts of Google Drive OAuth setup that gcloud CAN do.
# Standard OAuth Web Client creation still requires Google Cloud Console (no public API).
#
# Usage:
#   ./scripts/gdrive-gcp-bootstrap.sh PROJECT_ID REDIRECT_URI
#
# Example:
#   ./scripts/gdrive-gcp-bootstrap.sh my-gcp-project \
#     'https://panel.example.com/api/backups/gdrive/oauth/callback'

set -euo pipefail

PROJECT_ID="${1:-}"
REDIRECT_URI="${2:-}"
DATA_DIR="${HSERVER_DATA_DIR:-/var/lib/hserver}"
ENV_FILE="${HSERVER_ENV_FILE:-/etc/hserver/hserver.env}"

if [[ -z "$PROJECT_ID" || -z "$REDIRECT_URI" ]]; then
  echo "Usage: $0 PROJECT_ID REDIRECT_URI" >&2
  exit 1
fi

if ! command -v gcloud >/dev/null 2>&1; then
  echo "gcloud CLI not found. Install: https://cloud.google.com/sdk/docs/install" >&2
  exit 1
fi

echo "==> Project: $PROJECT_ID"
gcloud config set project "$PROJECT_ID" >/dev/null

echo "==> Enabling Google Drive API..."
gcloud services enable drive.googleapis.com --project="$PROJECT_ID"

echo "==> Enabling People API (user profile email)..."
gcloud services enable people.googleapis.com --project="$PROJECT_ID" 2>/dev/null || true

CONSOLE_CREDS="https://console.cloud.google.com/apis/credentials?project=${PROJECT_ID}"
CREATE_CLIENT="https://console.cloud.google.com/auth/clients/create?project=${PROJECT_ID}"
DRIVE_API="https://console.cloud.google.com/apis/library/drive.googleapis.com?project=${PROJECT_ID}"

cat <<EOF

=== Otomatik adımlar tamamlandı ===

Google Console'da OAuth client oluşturma API ile yapılamıyor (Google issue #326950115).
Aşağıdaki tek seferlik manuel adımı tamamlayın:

1. OAuth client oluştur:
   ${CREATE_CLIENT}

   - Application type: Web application
   - Authorized redirect URI:
     ${REDIRECT_URI}

2. Client ID ve Client Secret'ı kopyalayın.

3. Sunucuya ekleyin (bir yöntem):

   A) Ortam değişkeni (${ENV_FILE}):
      HSERVER_GDRIVE_CLIENT_ID=....apps.googleusercontent.com
      HSERVER_GDRIVE_CLIENT_SECRET=GOCSPX-...

   B) Vendor dosyası (Plesk tarzı, chmod 600):
      ${DATA_DIR}/gdrive-vendor-oauth.json
      {"clientId":"...","clientSecret":"..."}

   C) Panel sihirbazı → "Kendi GCP projeniz" sekmesi

4. Servisi yeniden başlatın:
   systemctl restart hserver

Yardımcı bağlantılar:
  Credentials: ${CONSOLE_CREDS}
  Drive API:   ${DRIVE_API}

EOF

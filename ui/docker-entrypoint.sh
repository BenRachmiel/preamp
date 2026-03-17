#!/bin/sh
cat > /usr/share/nginx/html/config.js <<EOF
window.__PREAMP_CONFIG__ = {
  apiUrl: "${PREAMP_API_URL:-}",
  noAuth: ${PREAMP_NO_AUTH:-false},
  devUsername: "${PREAMP_DEV_USERNAME:-dev}"
};
EOF

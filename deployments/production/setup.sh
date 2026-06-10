#!/bin/bash
# AurelioMod — VPS Provisioning (Hetzner CPX31 / Ubuntu 24.04)
# Ejecutar UNA vez en el VPS nuevo como root.
# Ajustar DOMAIN e IP antes de ejecutar.

set -euo pipefail

DOMAIN="${DOMAIN:-api.aureliomod.com}"
IP=$(curl -s ifconfig.me)

echo "=== AurelioMod VPS Setup ==="
echo "Dominio: $DOMAIN"
echo "IP:      $IP"
echo ""

# 1. Actualizar sistema
apt update && apt upgrade -y

# 2. Instalar dependencias
apt install -y \
    ca-certificates curl gnupg lsb-release ufw \
    git make

# 3. Docker (oficial)
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
apt update && apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# 4. Firewall
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # HTTP (Caddy)
ufw allow 443/tcp   # HTTPS (Caddy)
ufw --force enable

# 5. Usuario de deploy
if ! id -u aurelio &>/dev/null; then
    useradd -m -s /bin/bash aurelio
    usermod -aG docker aurelio
    mkdir -p /home/aurelio/.ssh
    cp /root/.ssh/authorized_keys /home/aurelio/.ssh/authorized_keys 2>/dev/null || echo "  ⚠ Agregá tu clave SSH a /home/aurelio/.ssh/authorized_keys manualmente"
    chown -R aurelio:aurelio /home/aurelio/.ssh
    chmod 700 /home/aurelio/.ssh
    chmod 600 /home/aurelio/.ssh/authorized_keys
fi

# 6. Directorio de la app
APP_DIR="/home/aurelio/aureliomod"
if [ ! -d "$APP_DIR" ]; then
    git clone https://github.com/soyAurelio/AurelioMod.git "$APP_DIR"
    chown -R aurelio:aurelio "$APP_DIR"
fi

# 7. Crear .env si no existe
if [ ! -f "$APP_DIR/.env" ]; then
    cat > "$APP_DIR/.env" << 'EOF'
# === PRODUCCIÓN ===
# Completar con valores reales antes de deploy

# WaveSpeed AI
WAVESPEED_API_KEY=
WAVESPEED_PLAN=bronze

# Neon DB
NEON_DATABASE_URL=

# PASETO
PASETO_SECRET_KEY=

# Stripe (opcional — descomentar cuando esté listo)
# STRIPE_SECRET_KEY=sk_live_...
# STRIPE_WEBHOOK_SECRET=whsec_...
# STRIPE_PRICE_BRONZE=price_...
# STRIPE_PRICE_SILVER=price_...
# STRIPE_PRICE_GOLD=price_...
# CONTROL_BASE_URL=https://api.aureliomod.com

# Discord Bot
DISCORD_TOKEN=
REQUIRED_GUILD_ID=
ENFORCE_MODERATION=true
BOT_LANG=es

# Web Risk API (Google Cloud)
WEBRISK_API_KEY=
EOF
    chmod 600 "$APP_DIR/.env"
    echo "  ✅ .env creado — completalo con los valores de producción"
fi

echo ""
echo "=== Setup completado ==="
echo "Próximos pasos:"
echo "  1. Completar $APP_DIR/.env con valores reales"
echo "  2. Apuntar DNS: $DOMAIN → $IP"
echo "  3. Ejecutar: docker compose -f $APP_DIR/compose.yml -f $APP_DIR/deployments/production/compose.prod.yml up -d"
echo "  4. Verificar: curl https://$DOMAIN/healthz"

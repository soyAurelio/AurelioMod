#!/bin/bash
# AurelioMod — VPS Provisioning (Fase 2a: Vultr/Hetzner, Ubuntu 24.04)
# Ejecutar UNA vez en el VPS nuevo como root.
# Ajustar DOMAIN e IP antes de ejecutar.

set -euo pipefail

DOMAIN="${DOMAIN:-api.aureliomod.com}"
IP=$(curl -s ifconfig.me)
PROVIDER=$(curl -s metadata.vultr.com 2>/dev/null && echo "vultr" || echo "generic")

echo "=== AurelioMod VPS Setup ==="
echo "Proveedor: $PROVIDER"
echo "Dominio:   $DOMAIN"
echo "IP:        $IP"
echo ""

# 1. Actualizar sistema
apt update && apt upgrade -y

# 2. Swap 2GB — safety net contra OOM en VPS de 1GB
if [ ! -f /swapfile ]; then
    echo "Creando swap 2GB..."
    fallocate -l 2G /swapfile
    chmod 600 /swapfile
    mkswap /swapfile
    swapon /swapfile
    echo '/swapfile none swap sw 0 0' >> /etc/fstab
    echo "  ✅ Swap 2GB activado"
else
    echo "  ⏭  Swap ya existe"
fi

# 3. Instalar dependencias
apt install -y ca-certificates curl gnupg lsb-release ufw git make

# 4. Docker (oficial)
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
apt update && apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# 5. Firewall
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # HTTP (Caddy)
ufw allow 443/tcp   # HTTPS (Caddy)
ufw --force enable

# 6. Usuario de deploy
if ! id -u aurelio &>/dev/null; then
    useradd -m -s /bin/bash aurelio
    usermod -aG docker aurelio
    mkdir -p /home/aurelio/.ssh
    cp /root/.ssh/authorized_keys /home/aurelio/.ssh/authorized_keys 2>/dev/null || echo "  ⚠ Agregá tu clave SSH a /home/aurelio/.ssh/authorized_keys manualmente"
    chown -R aurelio:aurelio /home/aurelio/.ssh
    chmod 700 /home/aurelio/.ssh
    chmod 600 /home/aurelio/.ssh/authorized_keys
fi

# 7. Directorio de la app
APP_DIR="/home/aurelio/aureliomod"
if [ ! -d "$APP_DIR" ]; then
    git clone https://github.com/soyAurelio/AurelioMod.git "$APP_DIR"
    chown -R aurelio:aurelio "$APP_DIR"
fi

# 8. Copiar .env.example → .env si no existe
if [ ! -f "$APP_DIR/.env" ]; then
    if [ -f "$APP_DIR/.env.example" ]; then
        cp "$APP_DIR/.env.example" "$APP_DIR/.env"
        chmod 600 "$APP_DIR/.env"
        echo "  ✅ .env creado desde .env.example — completalo con valores reales"
    else
        echo "  ⚠ .env.example no encontrado — creá .env manualmente"
    fi
fi

# 9. Weaviate Cloud schema
if [ -n "${WEAVIATE_ADDR:-}" ] && [ -n "${WEAVIATE_API_KEY:-}" ]; then
    echo "Creando schema Weaviate Cloud..."
    curl -sf -X POST "$WEAVIATE_ADDR/v1/schema" \
        -H "Authorization: Bearer $WEAVIATE_API_KEY" \
        -H "Content-Type: application/json" \
        -d "@$APP_DIR/deployments/weaviate-schema.json" \
        && echo "  ✅ Schema ModeratedContent creado" \
        || echo "  ⚠ Schema ya existe o falló la creación (verificar credenciales)"
else
    echo "  ⏭  WEAVIATE_ADDR/API_KEY no definidos — schema manual después"
fi

echo ""
echo "=== Setup completado ==="
echo "Próximos pasos:"
echo "  1. Completar $APP_DIR/.env con valores reales"
echo "  2. Apuntar DNS: $DOMAIN → $IP"
echo "  3. cd $APP_DIR && docker compose -f compose.yml -f compose.prod.yml build"
echo "  4. cd $APP_DIR && docker compose -f compose.yml -f compose.prod.yml up -d"
echo "  5. Verificar: curl https://$DOMAIN/healthz"

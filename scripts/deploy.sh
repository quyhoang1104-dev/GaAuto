#!/usr/bin/env bash
# =====================================================================
# deploy.sh — Production Deployment Script for GoClaw on VPS
# Executed locally on VPS or triggered remotely via GitHub Actions
# =====================================================================
set -euo pipefail

APP_DIR="/opt/goclaw"
cd "$APP_DIR"

echo "=== [1/5] Pulling latest changes from Git ==="
git fetch origin main
git reset --hard origin/main

echo "=== [2/5] Checking environment configuration ==="
if [ ! -f .env ]; then
  echo "Error: .env file not found in $APP_DIR! Generating a default .env..."
  ./prepare-env.sh
fi

echo "=== [3/5] Pulling latest Docker images ==="
docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml pull

echo "=== [4/5] Deploying GoClaw & PostgreSQL containers ==="
docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml up -d --remove-orphans

echo "=== [5/5] Checking container status and health ==="
sleep 5
docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml ps

# Health check GoClaw HTTP endpoint
for i in {1..12}; do
  if curl -s -f http://127.0.0.1:18790/ > /dev/null 2>&1; then
    echo " GoClaw is healthy and responding on http://127.0.0.1:18790"
    break
  fi
  echo "Waiting for GoClaw gateway to initialize... ($i/12)"
  sleep 3
done

# Prune unused images to save disk space on VPS
docker image prune -f

echo "=== Deployment completed successfully! ==="

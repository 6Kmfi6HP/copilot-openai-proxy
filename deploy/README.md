# Deployment Profiles

This directory contains three deployment examples for the same image:

- `compose/`: single-host Docker Compose
- `swarm/`: single-manager Docker Swarm stack
- `k3s/`: single-node K3s manifests

All three formats support outbound proxy settings through `PROXY_URL` or the standard proxy environment variables.

## Compose

```bash
cp deploy/compose/.env.example deploy/compose/.env
docker compose --env-file deploy/compose/.env -f deploy/compose/docker-compose.yml up -d
```

## Swarm

```bash
docker swarm init
cp deploy/swarm/.env.example deploy/swarm/.env
set -a
source deploy/swarm/.env
set +a
docker stack deploy -c deploy/swarm/docker-stack.yml copilot
```

## K3s

```bash
sudo k3s kubectl apply -k deploy/k3s
```

Before deploying K3s, edit `deploy/k3s/secret.example.yaml` and `deploy/k3s/configmap.yaml`.

The single-node K3s example exposes the API through `NodePort 30080`.

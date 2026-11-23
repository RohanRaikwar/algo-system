# Telegram → Angel One latency: infrastructure

The code path from a Telegram "Active" to the order request is a few milliseconds. The network hops around it are tens to hundreds. This page lists the infrastructure changes that matter, largest first. Measure before and after with the `⏱` log lines and the `orderpath_stage_ms` histogram (see "Measuring").

## Budget (activation reply → order at Angel)

| Stage | Typical | Controlled by |
|---|---|---|
| Telegram DC → our client | 20–150 ms | server region vs Telegram DC |
| tgsignal read + publish | < 1 ms | code |
| Redis publish → stratengine | 0.5–2 ms private network, 5–30 ms public proxy | `REDIS_ADDR` |
| stratengine → order worker → executor | < 1 ms idle | code |
| Journal fsync + executor snapshot | 1–10 ms | disk, Redis |
| HTTPS request → Angel One ack | ~10 ms from Mumbai, ~40–60 ms from Singapore, 200+ ms from US/EU | server region |
| Angel One → exchange | 50–200 ms | Angel |

## 1. Region (largest controllable cost)

Angel One's API (`apiconnect.angelone.in`) and NSE/BSE are in Mumbai. Railway has no India region; its nearest is `asia-southeast` (Singapore).

- Best: run the stack on a Mumbai VM (AWS `ap-south-1` or GCP `asia-south1`) with Redis on the same VM (`REDIS_ADDR=localhost:6379`).
- Minimum: set the Railway service and the Redis service to the Singapore region.
- Check the region today in the Railway dashboard (Service → Settings → Region).

## 2. Static IP

SEBI's retail algo rules route API orders through a static IP registered with the broker. Confirm the requirement and registration steps with Angel One.

- Railway's outbound IP is shared and can change; a VM with an Elastic/static IP gives a fixed address.
- Set `ANGEL_CLIENT_PUBLIC_IP=<registered IP>`. It is sent as `X-ClientPublicIP` and replaces the `api.ipify.org` lookup (and its hard-coded fallback) on every login and session refresh.

## 3. Redis on the private network

`REDIS_ADDR` must point at the private host (`redis.railway.internal:6379` on Railway, `localhost:6379` on a VM), never the public TCP proxy. Every call, follow-up and dashboard update crosses it.

## 4. CPU

About eight Go processes share one container. Each sizes its scheduler by the host CPU count, not the container limit, which can cause CPU throttling. Set per-process limits in `backend/docker-entrypoint.sh` once the container's CPU count is known, for example:

```
GOMAXPROCS=2 /app/bin/stratengine &
GOMAXPROCS=1 /app/bin/indengine &
```

and a `GOMEMLIMIT` a little under each process's share of the container memory.

## 5. Telegram DC

tgsignal logs `Telegram DC <n>` at login. The account's home DC is fixed by Telegram; for Indian numbers it is usually DC 5 (Singapore). A server in Singapore or Mumbai is close to it either way.

## Measuring

- tgsignal → stratengine, per call and follow-up: `⏱ telegram follow-up … sent→recv … recv→publish … publish→here …` (Telegram's sent time has 1-second resolution).
- Executor, per real order: `⏱ BUY <strategy> <symbol>: signal→executor … | journal … | persist … | send→ack …`.
- Exit by stop modify: `⚡ EXIT … modified … in Nms`.
- Prometheus (stratengine metrics endpoint): `orderpath_stage_ms{stage=tg_sent_to_recv|tg_recv_to_publish|tg_publish_to_engine|signal_to_executor|journal|persist|place_ack|exit_modify_ack}`.

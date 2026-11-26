#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════
#  Telegram session for tgsignal: login, check, encrypted backup,
#  restore, and push to Railway. Secrets never enter git.
#
#  ./scripts/tg_session.sh login   [name]   QR login → check → backup
#  ./scripts/tg_session.sh check   [name]   verify session + TG_CHAT_IDS
#  ./scripts/tg_session.sh backup  [name]   encrypt session → ~/algo-secrets/tg-<name>.session.b64.gpg
#  ./scripts/tg_session.sh restore [name]   decrypt backup → backend/data/tg-<name>.session
#  ./scripts/tg_session.sh railway [name] <project> <service> [environment]
#                                           set TG_SESSION_B64 on Railway from the backup
#
#  name defaults to "prod". TG_API_ID / TG_API_HASH / TG_CHAT_IDS are read
#  from .env.prod (override with TG_ENV_FILE). The backup passphrase lives
#  in ~/.config/algo/tg-backup.pass (created on first backup, mode 600);
#  keep a copy of it in your password manager — without it the backup
#  cannot be decrypted. Run one session in one place at a time.
# ═══════════════════════════════════════════════════════════════════
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BACKEND="$ROOT/backend"
SECRETS="${TG_SECRETS_DIR:-$HOME/algo-secrets}"
PASSFILE="${TG_BACKUP_PASSPHRASE_FILE:-$HOME/.config/algo/tg-backup.pass}"
ENV_FILE="${TG_ENV_FILE:-$ROOT/.env.prod}"

cmd="${1:-}"; name="${2:-prod}"
SESSION="$BACKEND/data/tg-$name.session"
BACKUP="$SECRETS/tg-$name.session.b64.gpg"

die() { echo "❌ $*" >&2; exit 1; }

# load_tg_env exports only the Telegram variables from ENV_FILE.
load_tg_env() {
  [ -f "$ENV_FILE" ] || die "no $ENV_FILE (set TG_ENV_FILE)"
  local k v
  for k in TG_API_ID TG_API_HASH TG_CHAT_IDS TG_PASSWORD; do
    v="$(grep -E "^$k=" "$ENV_FILE" | tail -1 | cut -d= -f2- | sed -E 's/^"(.*)"$/\1/')" || true
    [ -n "$v" ] && export "$k=$v"
  done
  [ -n "${TG_API_ID:-}" ] && [ -n "${TG_API_HASH:-}" ] || die "TG_API_ID / TG_API_HASH missing in $ENV_FILE"
  export TG_SESSION_PATH="$SESSION"
}

ensure_passfile() {
  if [ ! -s "$PASSFILE" ]; then
    mkdir -p "$(dirname "$PASSFILE")"; chmod 700 "$(dirname "$PASSFILE")"
    ( umask 077; openssl rand -base64 33 > "$PASSFILE" )
    echo "🔑 new backup passphrase: $PASSFILE — copy it to your password manager now."
  fi
  chmod 600 "$PASSFILE"
}

decrypt() {
  [ -f "$BACKUP" ] || die "no backup $BACKUP"
  [ -s "$PASSFILE" ] || die "no passphrase file $PASSFILE"
  gpg --batch --quiet --pinentry-mode loopback --passphrase-file "$PASSFILE" -d "$BACKUP"
}

do_check() {
  load_tg_env
  [ -f "$SESSION" ] || die "no session $SESSION (run: $0 login $name)"
  (cd "$BACKEND" && go run ./cmd/tools/tglogin -check)
}

do_backup() {
  load_tg_env
  [ -f "$SESSION" ] || die "no session $SESSION"
  ensure_passfile
  mkdir -p "$SECRETS"; chmod 700 "$SECRETS"
  (cd "$BACKEND" && go run ./cmd/tools/tglogin -export) |
    gpg --batch --yes --quiet --pinentry-mode loopback --passphrase-file "$PASSFILE" \
        --symmetric --cipher-algo AES256 -o "$BACKUP"
  chmod 600 "$BACKUP"
  decrypt | base64 -d | cmp -s - "$SESSION" || die "backup does not decrypt to the session — kept old files"
  echo "✅ encrypted backup: $BACKUP"
  # Remove older plain-text copies.
  for f in "$SECRETS/tg-$name.session.b64" "$SECRETS/tg-$name.session"; do
    [ -f "$f" ] && shred -u "$f" && echo "🧹 removed plain copy $f"
  done
  return 0
}

do_login() {
  load_tg_env
  if [ -f "$SESSION" ]; then
    read -r -p "Session $SESSION exists. Replace it? [y/N] " a
    [ "$a" = "y" ] || exit 0
    rm -f "$SESSION"
  fi
  local png="$BACKEND/data/tg-login-qr.png" big="$BACKEND/data/tg-login-qr-BIG.png"
  rm -f "$png" "$big"
  # Keep a large, scannable copy of the QR (it refreshes every ~30 s).
  python3 - "$png" "$big" <<'PY' &
import os, sys, time
src, dst = sys.argv[1], sys.argv[2]
try:
    from PIL import Image
except ImportError:
    sys.exit(0)
last, end = 0, time.time() + 600
while time.time() < end:
    try:
        m = os.path.getmtime(src)
        if m != last:
            last = m
            im = Image.open(src).convert('L')
            box = Image.eval(im, lambda p: 255 - p).getbbox()
            if box: im = im.crop(box)
            im = im.resize((im.width * 16, im.height * 16), Image.NEAREST)
            out = Image.new('L', (im.width + 128, im.height + 128), 255)
            out.paste(im, (64, 64)); out.save(dst)
    except FileNotFoundError:
        if last: break
    except Exception:
        pass
    time.sleep(0.5)
PY
  local watcher=$!
  ( sleep 3; [ -f "$big" ] && command -v xdg-open >/dev/null && xdg-open "$big" >/dev/null 2>&1 || true ) &
  echo "📱 Telegram → Settings → Devices → Link Desktop Device → scan $big"
  (cd "$BACKEND" && go run ./cmd/tools/tglogin -qr -qr-png "$png") || { kill $watcher 2>/dev/null; die "login failed"; }
  kill $watcher 2>/dev/null || true
  rm -f "$png" "$big"
  chmod 600 "$SESSION"
  do_check || echo "⚠️  check failed — the account may be missing a group in TG_CHAT_IDS"
  do_backup
}

do_restore() {
  mkdir -p "$BACKEND/data"
  ( umask 077; decrypt | base64 -d > "$SESSION" )
  echo "✅ restored $SESSION"
}

do_railway() {
  local project="${3:-}" service="${4:-}" envname="${5:-production}"
  [ -n "$project" ] && [ -n "$service" ] || die "usage: $0 railway [name] <project> <service> [environment]"
  command -v railway >/dev/null || die "railway CLI not installed"
  # Value goes through stdin, never on a command line (ps would show it).
  decrypt | railway variable set TG_SESSION_B64 --stdin -p "$project" -s "$service" -e "$envname" >/dev/null
  echo "✅ TG_SESSION_B64 set on $project/$service ($envname). Stop tgsignal on any other VM first."
}

case "$cmd" in
  login)   do_login ;;
  check)   do_check ;;
  backup)  do_backup ;;
  restore) do_restore ;;
  railway) do_railway "$@" ;;
  *) sed -n '3,20p' "$0"; exit 1 ;;
esac

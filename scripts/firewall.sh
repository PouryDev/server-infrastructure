#!/usr/bin/env bash
# Only let the CDN (and Docker's own networks) reach the published HTTP port.
#
# Why this is needed: the CDN connects straight to this server's IP, so anyone who learns the IP
# can bypass the CDN/WAF. And ufw does NOT filter Docker-published ports (Docker inserts its own
# iptables rules first), so the filter has to live in the DOCKER-USER chain.
#
#   scripts/firewall.sh              print the rules that would be applied (dry run)
#   sudo scripts/firewall.sh --apply apply them now
#   sudo scripts/firewall.sh --remove  remove them
#
# Persist across reboots/Docker restarts: host/infra-firewall.service (see README).
# Run scripts/update-arvan-ips.sh first to refresh config/firewall/cdn-ips.txt.
set -euo pipefail
cd "$(dirname "$0")/.."

MODE="${1:-}"
PORT="${HTTP_PORT:-$(grep -E '^NGINX_HTTP_PORT=' .env 2>/dev/null | cut -d= -f2 || true)}"; PORT="${PORT:-80}"
WAN_IF="${WAN_IF:-$(ip route show default | awk '{print $5; exit}')}"
CHAIN=INFRA-CDN
[ -n "$WAN_IF" ] || { echo "cannot detect the public interface; set WAN_IF=eth0" >&2; exit 1; }
mapfile -t CIDRS < <(grep -E '^[0-9]' config/firewall/cdn-ips.txt)
[ "${#CIDRS[@]}" -ge 5 ] || { echo "config/firewall/cdn-ips.txt looks empty" >&2; exit 1; }

rules() {
  echo "iptables -N $CHAIN 2>/dev/null || iptables -F $CHAIN"
  for c in "${CIDRS[@]}"; do echo "iptables -A $CHAIN -s $c -j RETURN"; done
  # container-to-container and host-local traffic must keep working
  for c in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16; do echo "iptables -A $CHAIN -s $c -j RETURN"; done
  echo "iptables -A $CHAIN -j DROP"
  echo "while iptables -D DOCKER-USER -i $WAN_IF -p tcp -m conntrack --ctorigdstport $PORT --ctdir ORIGINAL -j $CHAIN 2>/dev/null; do :; done"
  echo "iptables -I DOCKER-USER 1 -i $WAN_IF -p tcp -m conntrack --ctorigdstport $PORT --ctdir ORIGINAL -j $CHAIN"
}

remove() {
  echo "while iptables -D DOCKER-USER -i $WAN_IF -p tcp -m conntrack --ctorigdstport $PORT --ctdir ORIGINAL -j $CHAIN 2>/dev/null; do :; done"
  echo "iptables -F $CHAIN 2>/dev/null; iptables -X $CHAIN 2>/dev/null; true"
}

case "$MODE" in
  --apply)
    [ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
    iptables -n -L DOCKER-USER >/dev/null 2>&1 || { echo "DOCKER-USER chain missing: is Docker running?" >&2; exit 1; }
    rules | bash -e
    echo "applied: port $PORT on $WAN_IF accepts only CDN + private ranges (${#CIDRS[@]} CDN ranges)" ;;
  --remove)
    [ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
    remove | bash -e; echo "removed" ;;
  "")
    echo "# dry run - nothing changed. Re-run with --apply (as root) to apply."
    rules ;;
  *) echo "usage: $0 [--apply|--remove]" >&2; exit 1 ;;
esac

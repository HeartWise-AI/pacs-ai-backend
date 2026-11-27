#!/bin/sh
# moby/moby#49325 guard (Docker < 28): hosts on the same L2 segment can reach
# container ports published on 127.0.0.1 by sending packets addressed to
# 127.0.0.1. Drop any forwarded packet whose original destination was loopback.
# Legitimate host-local clients use docker-proxy/OUTPUT and never hit FORWARD.
# Run as root after Docker starts (e.g. a systemd timer). Idempotent; flushes nothing.
# Do not add this through `firewall-cmd --direct`: on firewalld hosts that wipes
# Docker's own FORWARD rules until `firewall-cmd --reload`.
RULE="-m conntrack --ctorigdst 127.0.0.0/8 -m comment --comment moby-49325-loopback-guard -j DROP"
iptables -nL DOCKER-USER >/dev/null 2>&1 || exit 0   # docker not up yet
iptables -C DOCKER-USER $RULE 2>/dev/null || iptables -I DOCKER-USER 1 $RULE

#!/bin/sh
# Cloudflared status check script for OPNsense
# Shows service status, version, and recent logs

echo "=== Cloudflared Status ==="
echo "Service status:"
sudo service cloudflared status
echo

echo "Current version:"
cloudflared --version
echo

echo "Configuration:"
echo "Token file: $(sudo test -f /usr/local/etc/cloudflared/token && echo 'Present' || echo 'Missing')"
echo "RC script: $(sudo test -f /usr/local/etc/rc.d/cloudflared && echo 'Present' || echo 'Missing')"
echo "Update script: $(sudo test -f /usr/local/bin/update-cloudflared.sh && echo 'Present' || echo 'Missing')"
echo

echo "Recent logs:"
sudo tail -10 /var/log/cloudflared.log
echo

echo "Update logs (if any):"
sudo tail -5 /var/log/cloudflared-update.log 2>/dev/null || echo "No update logs yet"
echo

echo "Active connections (if available):"
# Try to get tunnel info if possible
curl -s http://127.0.0.1:20241/metrics | grep -E "(tunnel_|cloudflared_)" | head -5 || echo "Metrics not accessible"
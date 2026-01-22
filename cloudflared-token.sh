#!/bin/sh
# Cloudflared token setup script for OPNsense
# Store the tunnel token securely
#
# Usage: CLOUDFLARED_TOKEN="your-token-here" ./cloudflared-token.sh
# Or:    ./cloudflared-token.sh "your-token-here"
#
# Never store tokens in plain text files or scripts.

set -e

# Get token from environment variable or command line argument
if [ -n "$1" ]; then
    TOKEN="$1"
elif [ -n "$CLOUDFLARED_TOKEN" ]; then
    TOKEN="$CLOUDFLARED_TOKEN"
else
    echo "Error: Token not provided. Set CLOUDFLARED_TOKEN environment variable or pass as argument."
    echo "Usage: CLOUDFLARED_TOKEN=\"your-token\" $0"
    echo "   Or: $0 \"your-token\""
    exit 1
fi

# Validate token format (basic check)
if ! echo "$TOKEN" | grep -q "^eyJ"; then
    echo "Error: Token does not appear to be a valid JWT token"
    exit 1
fi

# Create secure config directory
sudo mkdir -p /usr/local/etc/cloudflared

# Store token securely (without echoing to console)
echo "$TOKEN" | sudo tee /usr/local/etc/cloudflared/token > /dev/null

# Set proper permissions
sudo chmod 600 /usr/local/etc/cloudflared/token
sudo chown root:wheel /usr/local/etc/cloudflared/token

echo "Cloudflared token stored securely at /usr/local/etc/cloudflared/token"
echo "Token length: ${#TOKEN} characters"
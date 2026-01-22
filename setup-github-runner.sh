#!/bin/sh
# Setup GitHub self-hosted runner on freebsd-dev
# This script configures freebsd-dev as a GitHub Actions runner
# for automated cloudflared builds
#
# Run this on freebsd-dev as root

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_FILE="/var/log/github-runner-setup.log"

# Configuration - CHANGE THESE
GITHUB_REPO="${GITHUB_REPO:-agoodkind/cloudflared-opnsense}"  # GitHub repository for this project
RUNNER_TOKEN="${GITHUB_RUNNER_TOKEN:-}"  # Get from GitHub Settings > Actions > Runners

# Log function
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $*" | tee -a "$LOG_FILE"
}

log "Starting GitHub runner setup for $GITHUB_REPO"

# Check if running on FreeBSD
if [ "$(uname)" != "FreeBSD" ]; then
    log "Error: This script must be run on FreeBSD"
    exit 1
fi

# Check required tools
for cmd in curl jq git; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        log "Installing missing dependency: $cmd"
        pkg install -y "$cmd"
    fi
done

# Check configuration
if [ -z "$GITHUB_REPO" ] || [ "$GITHUB_REPO" = "your-org/your-repo" ]; then
    log "Error: Please set GITHUB_REPO environment variable"
    log "Example: export GITHUB_REPO='yourusername/cloudflared-opnsense'"
    exit 1
fi

if [ -z "$RUNNER_TOKEN" ]; then
    log "Error: Please set GITHUB_RUNNER_TOKEN environment variable"
    log "Get token from: https://github.com/$GITHUB_REPO/settings/actions/runners"
    exit 1
fi

# Setup runner directory
RUNNER_DIR="/opt/actions-runner"
if [ -d "$RUNNER_DIR" ]; then
    log "Runner directory already exists, cleaning up..."
    rm -rf "$RUNNER_DIR"
fi

log "Creating runner directory: $RUNNER_DIR"
mkdir -p "$RUNNER_DIR"
cd "$RUNNER_DIR"

# Get latest runner version
log "Getting latest GitHub runner version"
RUNNER_VERSION=$(curl -s https://api.github.com/repos/actions/runner/releases/latest | jq -r '.tag_name' | sed 's/v//')

if [ -z "$RUNNER_VERSION" ]; then
    log "Error: Could not get runner version"
    exit 1
fi

log "Latest runner version: $RUNNER_VERSION"

# Download runner
RUNNER_ARCHIVE="actions-runner-freebsd-x64-${RUNNER_VERSION}.tar.gz"
RUNNER_URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${RUNNER_ARCHIVE}"

log "Downloading runner from $RUNNER_URL"
curl -L -o "$RUNNER_ARCHIVE" "$RUNNER_URL"

# Extract runner
log "Extracting runner"
tar xzf "$RUNNER_ARCHIVE"
rm "$RUNNER_ARCHIVE"

# Configure runner
log "Configuring runner"
./config.sh --url "https://github.com/$GITHUB_REPO" \
            --token "$RUNNER_TOKEN" \
            --name "freebsd-dev-$(hostname)" \
            --labels "freebsd,self-hosted" \
            --unattended \
            --replace

# Create service user if it doesn't exist
if ! id -u actions-runner >/dev/null 2>&1; then
    log "Creating actions-runner user"
    pw useradd actions-runner -m -s /bin/sh -c "GitHub Actions Runner"
fi

# Change ownership
chown -R actions-runner:actions-runner "$RUNNER_DIR"

# Create systemd service (FreeBSD rc script)
SERVICE_FILE="/usr/local/etc/rc.d/github-runner"
cat > "$SERVICE_FILE" << EOF
#!/bin/sh

# PROVIDE: github-runner
# REQUIRE: NETWORKING SERVERS
# KEYWORD: shutdown

. /etc/rc.subr

name="github_runner"
rcvar="github_runner_enable"

github_runner_user="actions-runner"
github_runner_chdir="$RUNNER_DIR"

command="/usr/sbin/daemon"
command_args="-u \${github_runner_user} -c \${github_runner_chdir} ./runsvc.sh"

load_rc_config \$name

: \${github_runner_enable:="NO"}

run_rc_command "\$1"
EOF

chmod 755 "$SERVICE_FILE"

# Enable and start service
log "Enabling and starting GitHub runner service"
sysrc github_runner_enable=YES
service github-runner start

log "Setup complete!"
log ""
log "GitHub runner is now configured and running."
log ""
log "To check status:"
log "  sudo service github-runner status"
log ""
log "To view logs:"
log "  tail -f $RUNNER_DIR/_diag/*.log"
log ""
log "The runner will automatically pick up jobs from GitHub Actions"
log "when workflows specify 'runs-on: [self-hosted, freebsd]'"
log ""
log "Make sure your repository has the following secrets:"
log "- GITHUB_RUNNER_TOKEN (for initial setup - can be removed after)"
log ""
log "And that workflows use:"
log "runs-on: [self-hosted, freebsd]"
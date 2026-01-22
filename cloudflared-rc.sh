#!/bin/sh
# FreeBSD RC script for cloudflared service with self-updating enabled
# Install this as /usr/local/etc/rc.d/cloudflared

sudo tee /usr/local/etc/rc.d/cloudflared > /dev/null << 'EOF'
#!/bin/sh

# PROVIDE: cloudflared
# REQUIRE: NETWORKING SERVERS
# KEYWORD: shutdown

. /etc/rc.subr

name="cloudflared"
rcvar="cloudflared_enable"
logfile="/var/log/cloudflared.log"
pidfile="/var/run/cloudflared.pid"
procname="/usr/local/bin/cloudflared"

load_rc_config $name

: ${cloudflared_enable:="NO"}
: ${cloudflared_mode:="tunnel"}

# Load token from secure file
if [ -f /usr/local/etc/cloudflared/token ]; then
    token=$(cat /usr/local/etc/cloudflared/token)
    command_args="${cloudflared_mode} --token ${token}"
else
    command_args="${cloudflared_mode}"
fi

command="/usr/sbin/daemon"
command_args="-o ${logfile} -p ${pidfile} -f ${procname} ${command_args}"

run_rc_command "$1"
EOF

# Make executable
sudo chmod 755 /usr/local/etc/rc.d/cloudflared

echo "Cloudflared RC script installed at /usr/local/etc/rc.d/cloudflared"
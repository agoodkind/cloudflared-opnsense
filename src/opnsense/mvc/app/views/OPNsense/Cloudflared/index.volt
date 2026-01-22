{#
# Copyright (C) 2025 Your Name
# All rights reserved.
#}

<script>
$(document).ready(function() {
    // Cloudflared status check
    function updateStatus() {
        $.ajax({
            url: '/api/cloudflared/settings/status',
            type: 'GET',
            success: function(data) {
                if (data.status === 'running') {
                    $('#status').removeClass('text-danger').addClass('text-success').text('Running');
                } else {
                    $('#status').removeClass('text-success').addClass('text-danger').text('Stopped');
                }
            }
        });
    }

    updateStatus();
    setInterval(updateStatus, 30000); // Update every 30 seconds
});
</script>

<div class="content-box">
    <div class="content-box-main">
        <div class="table-responsive">
            <table class="table table-striped">
                <thead>
                    <tr>
                        <th>Service</th>
                        <th>Status</th>
                        <th>Version</th>
                        <th>Actions</th>
                    </tr>
                </thead>
                <tbody>
                    <tr>
                        <td>Cloudflared Tunnel</td>
                        <td id="status">Checking...</td>
                        <td id="version">Unknown</td>
                        <td>
                            <button class="btn btn-xs btn-success" id="startBtn">
                                <i class="fa fa-play"></i>
                            </button>
                            <button class="btn btn-xs btn-danger" id="stopBtn">
                                <i class="fa fa-stop"></i>
                            </button>
                            <button class="btn btn-xs btn-info" id="restartBtn">
                                <i class="fa fa-refresh"></i>
                            </button>
                        </td>
                    </tr>
                </tbody>
            </table>
        </div>
    </div>
</div>

<div class="content-box">
    <div class="content-box-main">
        <h3>Configuration</h3>
        <p>Configure your Cloudflare tunnel settings in the <a href="/ui/cloudflared/settings">Settings</a> tab.</p>
    </div>
</div>
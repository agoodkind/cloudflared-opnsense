{#
# Copyright (C) 2025 Your Name
# All rights reserved.
#}

<script>
$(document).ready(function() {
    // Load current settings
    $.ajax({
        url: '/api/cloudflared/settings/get',
        type: 'GET',
        success: function(data) {
            // Populate form with current settings
            $('#enabled').prop('checked', data.settings.general.enabled === '1');
            $('#token').val(data.settings.general.token || '');
            $('#tunnel_name').val(data.settings.general.tunnel_name || '');
        }
    });

    // Save settings
    $('#saveBtn').click(function() {
        var settings = {
            settings: {
                general: {
                    enabled: $('#enabled').is(':checked') ? '1' : '0',
                    token: $('#token').val(),
                    tunnel_name: $('#tunnel_name').val()
                }
            }
        };

        $.ajax({
            url: '/api/cloudflared/settings/set',
            type: 'POST',
            data: JSON.stringify(settings),
            contentType: 'application/json',
            success: function(response) {
                if (response.result === 'saved') {
                    alert('Settings saved successfully');
                }
            }
        });
    });
});
</script>

<div class="content-box">
    <div class="content-box-main">
        <form id="settingsForm">
            <div class="form-group">
                <label for="enabled">
                    <input type="checkbox" id="enabled"> Enable Cloudflared
                </label>
            </div>

            <div class="form-group">
                <label for="token">Tunnel Token</label>
                <input type="password" class="form-control" id="token" placeholder="eyJ...">
                <small class="form-text text-muted">
                    Get your tunnel token from the Cloudflare dashboard
                </small>
            </div>

            <div class="form-group">
                <label for="tunnel_name">Tunnel Name</label>
                <input type="text" class="form-control" id="tunnel_name" placeholder="my-tunnel">
            </div>

            <button type="button" class="btn btn-primary" id="saveBtn">Save Settings</button>
        </form>
    </div>
</div>
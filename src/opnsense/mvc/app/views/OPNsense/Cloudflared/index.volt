{#
# Cloudflared Status View
# Copyright (C) 2025-2026
#}

<script>
    $(document).ready(function () {
        function updateStatus() {
            $.ajax({
                url: '/api/cloudflared/service/status',
                type: 'GET',
                success: function (data) {
                    if (data.status === 'running') {
                        $('#status')
                            .removeClass('label-danger label-default')
                            .addClass('label-success')
                            .text('{{ lang._('Running') }}');
                        $('#startBtn').prop('disabled', true);
                        $('#stopBtn').prop('disabled', false);
                        $('#restartBtn').prop('disabled', false);
                    } else {
                        $('#status')
                            .removeClass('label-success label-default')
                            .addClass('label-danger')
                            .text('{{ lang._('Stopped') }}');
                        $('#startBtn').prop('disabled', false);
                        $('#stopBtn').prop('disabled', true);
                        $('#restartBtn').prop('disabled', true);
                    }
                },
                error: function () {
                    $('#status')
                        .removeClass('label-success label-danger')
                        .addClass('label-default')
                        .text('{{ lang._('Unknown') }}');
                }
            });
        }

        function updateVersion() {
            $.ajax({
                url: '/api/cloudflared/service/version',
                type: 'GET',
                success: function (data) {
                    $('#version').text(data.version || '{{ lang._('Unknown') }}');
                }
            });
        }

        function serviceAction(action, btn) {
            var $btn = $(btn);
            $btn.prop('disabled', true);
            $.ajax({
                url: '/api/cloudflared/service/' + action,
                type: 'POST',
                success: function () {
                    setTimeout(updateStatus, 1000);
                },
                error: function () {
                    $btn.prop('disabled', false);
                }
            });
        }

        $('#startBtn').click(function () { serviceAction('start', this); });
        $('#stopBtn').click(function () { serviceAction('stop', this); });
        $('#restartBtn').click(function () { serviceAction('restart', this); });

        updateStatus();
        updateVersion();
        setInterval(updateStatus, 30000);
    });
</script>

<div class="content-box">
    <div class="content-box-header">
        <h3>{{ lang._('Cloudflared Tunnel Service') }}</h3>
    </div>
    <div class="content-box-main">
        <div class="table-responsive">
            <table class="table table-striped">
                <thead>
                    <tr>
                        <th>{{ lang._('Service') }}</th>
                        <th>{{ lang._('Status') }}</th>
                        <th>{{ lang._('Version') }}</th>
                        <th>{{ lang._('Actions') }}</th>
                    </tr>
                </thead>
                <tbody>
                    <tr>
                        <td>{{ lang._('Cloudflared Tunnel') }}</td>
                        <td>
                            <span id="status" class="label label-default">
                                {{ lang._('Checking...') }}
                            </span>
                        </td>
                        <td id="version">{{ lang._('Unknown') }}</td>
                        <td>
                            <button class="btn btn-xs btn-success" id="startBtn" title="{{ lang._('Start') }}">
                                <i class="fa fa-play"></i>
                            </button>
                            <button class="btn btn-xs btn-danger" id="stopBtn" title="{{ lang._('Stop') }}">
                                <i class="fa fa-stop"></i>
                            </button>
                            <button class="btn btn-xs btn-primary" id="restartBtn" title="{{ lang._('Restart') }}">
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
    <div class="content-box-header">
        <h3>{{ lang._('Quick Links') }}</h3>
    </div>
    <div class="content-box-main">
        <ul>
            <li>
                <a href="/ui/cloudflared/settings">
                    <i class="fa fa-cog"></i> {{ lang._('Configure Settings') }}
                </a>
            </li>
            <li>
                <a href="https://one.dash.cloudflare.com/" target="_blank">
                    <i class="fa fa-external-link"></i>
                    {{ lang._('Cloudflare Zero Trust Dashboard') }}
                </a>
            </li>
        </ul>
    </div>
</div>